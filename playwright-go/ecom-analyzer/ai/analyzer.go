package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"ecom-analyzer/scraper"
)

// AnalysisReport AI 返回的分析报告结构
type AnalysisReport struct {
	Keyword        string        `json:"keyword"`
	TotalProducts  int           `json:"total_products"`
	MarketOverview string        `json:"market_overview"`
	TopProducts    []ProductRank `json:"top_products"`
	Opportunities  []string      `json:"opportunities"`
	Risks          []string      `json:"risks"`
	Recommendation string        `json:"recommendation"`
}

type ProductRank struct {
	Title  string `json:"title"`
	Source string `json:"source"`
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

// Analyze 将抓取数据发给 AI 分析，返回 JSON 报告
func Analyze(keyword string, products []scraper.Product) (*AnalysisReport, error) {
	return AnalyzeWithTemplate(keyword, products, "")
}

// AnalyzeWithTemplate 支持自定义 Prompt 模板，模板为空时使用默认
func AnalyzeWithTemplate(keyword string, products []scraper.Product, tmpl string) (*AnalysisReport, error) {
	var prompt string
	if strings.TrimSpace(tmpl) != "" {
		prompt = buildCustomPrompt(keyword, products, tmpl)
	} else {
		prompt = buildPrompt(keyword, products)
	}

	// 优先使用 Gemini，其次 OpenAI
	var (
		raw string
		err error
	)
	if os.Getenv("GEMINI_API_KEY") != "" {
		raw, err = callGemini(prompt)
	} else if os.Getenv("OPENAI_API_KEY") != "" {
		raw, err = callOpenAI(prompt)
	} else {
		return nil, fmt.Errorf("请设置 GEMINI_API_KEY 或 OPENAI_API_KEY 环境变量")
	}
	if err != nil {
		return nil, err
	}

	return parseReport(raw)
}

// buildCustomPrompt 用用户自定义模板，支持 {{keyword}} {{products}} 占位符
func buildCustomPrompt(keyword string, products []scraper.Product, tmpl string) string {
	// 生成商品数据文本块
	var sb strings.Builder
	for i, p := range products {
		sb.WriteString(fmt.Sprintf("--- 商品 %d [%s] ---\n标题: %s\n价格: %s\n评分: %s\n评论数: %s\n", i+1, p.Source, p.Title, p.Price, p.Rating, p.ReviewCount))
		if p.Description != "" {
			desc := p.Description
			if len(desc) > 500 {
				desc = desc[:500] + "..."
			}
			sb.WriteString(fmt.Sprintf("描述: %s\n", desc))
		}
		sb.WriteString("\n")
	}
	result := strings.ReplaceAll(tmpl, "{{keyword}}", keyword)
	result = strings.ReplaceAll(result, "{{products}}", sb.String())
	return result
}

// buildPrompt 将商品数据拼接成结构化 Prompt
func buildPrompt(keyword string, products []scraper.Product) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`你是一位跨境电商选品专家。以下是关键词 "%s" 在 Amazon 和 TikTok Shop 上的热销商品数据：

`, keyword))

	for i, p := range products {
		sb.WriteString(fmt.Sprintf("--- 商品 %d [%s] ---\n", i+1, p.Source))
		sb.WriteString(fmt.Sprintf("标题: %s\n", p.Title))
		sb.WriteString(fmt.Sprintf("价格: %s\n", p.Price))
		sb.WriteString(fmt.Sprintf("评分: %s\n", p.Rating))
		sb.WriteString(fmt.Sprintf("评论数: %s\n", p.ReviewCount))
		if p.Description != "" {
			desc := p.Description
			if len(desc) > 500 {
				desc = desc[:500] + "..."
			}
			sb.WriteString(fmt.Sprintf("描述摘要: %s\n", desc))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(`
请基于以上数据，严格按照如下 JSON 格式输出选品分析报告（不要输出任何 JSON 以外的内容）：

{
  "keyword": "关键词",
  "total_products": 数量,
  "market_overview": "市场概况描述",
  "top_products": [
    {"title": "商品标题", "source": "平台", "score": 评分1-100, "reason": "推荐理由"}
  ],
  "opportunities": ["机会点1", "机会点2"],
  "risks": ["风险点1", "风险点2"],
  "recommendation": "综合选品建议"
}`)

	return sb.String()
}

// callGemini 调用 Google Gemini API
func callGemini(prompt string) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-flash:generateContent?key=%s", apiKey)

	body := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]string{{"text": prompt}}},
		},
		"generationConfig": map[string]any{
			"temperature":     0.3,
			"maxOutputTokens": 2048,
		},
	}

	return doPost(url, body, func(data []byte) (string, error) {
		var resp struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return "", err
		}
		if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
			return "", fmt.Errorf("Gemini 返回空结果")
		}
		return resp.Candidates[0].Content.Parts[0].Text, nil
	})
}

// callOpenAI 调用 OpenAI API
func callOpenAI(prompt string) (string, error) {
	url := "https://api.openai.com/v1/chat/completions"
	body := map[string]any{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.3,
	}

	return doPost(url, body, func(data []byte) (string, error) {
		var resp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("OpenAI 返回空结果")
		}
		return resp.Choices[0].Message.Content, nil
	})
}

// doPost 通用 HTTP POST，带超时
func doPost(url string, body any, parse func([]byte) (string, error)) (string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.Contains(url, "openai") {
		req.Header.Set("Authorization", "Bearer "+os.Getenv("OPENAI_API_KEY"))
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API 返回错误 %d: %s", resp.StatusCode, string(respData))
	}

	return parse(respData)
}

// parseReport 从 AI 返回文本中提取 JSON
func parseReport(raw string) (*AnalysisReport, error) {
	// 提取 JSON 块（AI 有时会在 JSON 前后加说明文字）
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("AI 返回内容中未找到有效 JSON:\n%s", raw)
	}
	jsonStr := raw[start : end+1]

	var report AnalysisReport
	if err := json.Unmarshal([]byte(jsonStr), &report); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w\n原始内容: %s", err, jsonStr)
	}
	return &report, nil
}
