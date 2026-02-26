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
	Title              string `json:"title"`
	Source             string `json:"source"`
	Price              string `json:"price"`
	Score              int    `json:"score"`   // 综合评分 1-100
	TechBarrier        int    `json:"tech_barrier"`        // 技术门槛 1-10（越低越好入场）
	SocialViralScore   int    `json:"social_viral_score"`  // 社交媒体传播力 1-10
	ProfitMarginScore  int    `json:"profit_margin_score"` // 利润率 1-10
	WorthFollowing     bool   `json:"worth_following"`     // 是否值得跟进
	Reason             string `json:"reason"`
}

// Analyze 将抓取数据发给 AI 分析，返回 JSON 报告
func Analyze(keyword string, products []scraper.Product) (*AnalysisReport, error) {
	return AnalyzeWithTemplate(keyword, products, "", "")
}

// AnalyzeWithTemplate 支持自定义 Prompt 模板和自定义端点
// endpoint 为空时按环境变量自动选择；非空时直接使用该 URL
func AnalyzeWithTemplate(keyword string, products []scraper.Product, tmpl, endpoint string) (*AnalysisReport, error) {
	var prompt string
	if strings.TrimSpace(tmpl) != "" {
		prompt = buildCustomPrompt(keyword, products, tmpl)
	} else {
		prompt = buildPrompt(keyword, products)
	}

	var (
		raw string
		err error
	)

	if endpoint != "" {
		// 用户自定义端点：根据 URL 特征自动选择请求格式
		raw, err = callEndpoint(endpoint, prompt)
	} else if os.Getenv("GEMINI_API_KEY") != "" {
		raw, err = callGemini(prompt)
	} else if os.Getenv("OPENAI_API_KEY") != "" {
		raw, err = callOpenAI(prompt)
	} else {
		return nil, fmt.Errorf("请填写 API Key 或自定义端点")
	}
	if err != nil {
		return nil, err
	}

	return parseReport(raw)
}

// callEndpoint 根据 URL 特征自动判断 Gemini / OpenAI 兼容格式
func callEndpoint(endpoint, prompt string) (string, error) {
	if isGeminiURL(endpoint) {
		return callGeminiURL(endpoint, prompt)
	}
	// 其余统一走 OpenAI Chat Completions 兼容格式（适用于 OpenAI / Azure / 本地 Ollama / 各类中转）
	return callOpenAIURL(endpoint, prompt)
}

func isGeminiURL(u string) bool {
	return strings.Contains(u, "generativelanguage.googleapis.com") ||
		strings.Contains(u, "generateContent")
}

// callGeminiURL 向指定 Gemini 端点发请求，自动处理 key 在 URL 还是 Header 里
func callGeminiURL(endpoint, prompt string) (string, error) {
	// 如果 URL 里没有 key= 参数，尝试从环境变量补上
	reqURL := endpoint
	if !strings.Contains(reqURL, "key=") && os.Getenv("GEMINI_API_KEY") != "" {
		sep := "?"
		if strings.Contains(reqURL, "?") {
			sep = "&"
		}
		reqURL = reqURL + sep + "key=" + os.Getenv("GEMINI_API_KEY")
	}

	body := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]string{{"text": prompt}}},
		},
		"generationConfig": map[string]any{
			"temperature":     0.3,
			"maxOutputTokens": 4096,
		},
	}

	return doPost(reqURL, body, parseGeminiResp)
}

// callOpenAIURL 向指定 OpenAI 兼容端点发请求
func callOpenAIURL(endpoint, prompt string) (string, error) {
	body := map[string]any{
		"model": "gpt-4o-mini", // 中转服务通常忽略此字段或自行路由
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.3,
	}

	return doPostWithAuth(endpoint, os.Getenv("OPENAI_API_KEY"), body, parseOpenAIResp)
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

	sb.WriteString(fmt.Sprintf(`# 角色
你是一位拥有 10 年经验的跨境电商选品专家，熟悉 Amazon、TikTok Shop 的流量规则、供应链成本结构和社交媒体营销趋势。

# 任务
分析关键词「%s」在 2026 年的跨境电商市场潜力。以下是从 Amazon / TikTok Shop 抓取的热销商品数据：

`, keyword))

	for i, p := range products {
		sb.WriteString(fmt.Sprintf("## 商品 %d [%s]\n", i+1, p.Source))
		sb.WriteString(fmt.Sprintf("- 标题: %s\n", p.Title))
		sb.WriteString(fmt.Sprintf("- 价格: %s\n", p.Price))
		if p.Rating != "" {
			sb.WriteString(fmt.Sprintf("- 评分: %s\n", p.Rating))
		}
		if p.ReviewCount != "" {
			sb.WriteString(fmt.Sprintf("- 评论数: %s\n", p.ReviewCount))
		}
		if p.Brand != "" {
			sb.WriteString(fmt.Sprintf("- 品牌: %s\n", p.Brand))
		}
		if p.IsPrime {
			sb.WriteString("- Prime: 是\n")
		}
		if p.Description != "" {
			desc := p.Description
			if len(desc) > 600 {
				desc = desc[:600] + "..."
			}
			sb.WriteString(fmt.Sprintf("- 描述摘要: %s\n", desc))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(`# 评分维度说明
对每个商品从以下三个维度打分（1-10 分）：

1. **技术门槛**（tech_barrier）：1 = 完全无门槛、任何工厂都能做；10 = 需要专利/芯片/复杂工艺。
   - 评分越低，说明入场越容易，竞争也越激烈；评分越高，说明护城河强但供应链难度大。

2. **社交媒体传播力**（social_viral_score）：1 = 纯功能品、难以制造内容；10 = 天然适合短视频/开箱/测评，自带话题性。
   - 重点考量：视觉冲击力、使用场景是否有趣、是否能引发情绪共鸣。

3. **利润率**（profit_margin_score）：1 = 红海价格战、利润极薄；10 = 高溢价空间、差异化明显。
   - 综合考量：售价区间、同类竞品密度、品牌溢价可能性、物流成本（体积/重量）。

# 输出要求
严格按照以下 JSON 格式输出，不要输出任何 JSON 以外的内容：

{
  "keyword": "关键词",
  "total_products": 商品数量,
  "market_overview": "100字以内的市场整体判断，包括竞争格局、需求趋势、2026年机会窗口",
  "top_products": [
    {
      "title": "商品标题",
      "source": "Amazon 或 TikTokShop",
      "price": "价格",
      "tech_barrier": 技术门槛评分,
      "social_viral_score": 社交传播力评分,
      "profit_margin_score": 利润率评分,
      "score": 三项均值×10后取整的综合分,
      "worth_following": true或false,
      "reason": "50字以内，说明值得/不值得跟进的核心理由"
    }
  ],
  "opportunities": ["具体机会点，结合2026年趋势，至少3条"],
  "risks": ["具体风险点，至少2条"],
  "recommendation": "150字以内的综合选品建议，给出明确的行动指引"
}`)

	return sb.String()
}

// callGemini 调用 Google Gemini API（使用环境变量 key）
func callGemini(prompt string) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent?key=%s", apiKey)
	body := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]string{{"text": prompt}}},
		},
		"generationConfig": map[string]any{
			"temperature":     0.3,
			"maxOutputTokens": 4096,
		},
	}
	return doPost(url, body, parseGeminiResp)
}

func parseGeminiResp(data []byte) (string, error) {
	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("Gemini API 错误: %s", resp.Error.Message)
	}
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("Gemini 返回空结果")
	}
	return resp.Candidates[0].Content.Parts[0].Text, nil
}

// callOpenAI 调用 OpenAI API（使用环境变量 key）
func callOpenAI(prompt string) (string, error) {
	return callOpenAIURL("https://api.openai.com/v1/chat/completions", prompt)
}

func parseOpenAIResp(data []byte) (string, error) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("OpenAI API 错误: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("OpenAI 返回空结果")
	}
	return resp.Choices[0].Message.Content, nil
}

// doPost 通用 HTTP POST（无 Authorization header）
func doPost(url string, body any, parse func([]byte) (string, error)) (string, error) {
	return doPostWithAuth(url, "", body, parse)
}

// doPostWithAuth 通用 HTTP POST，bearer 非空时加 Authorization header
func doPostWithAuth(url, bearer string, body any, parse func([]byte) (string, error)) (string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 90 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
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
		return "", fmt.Errorf("API 返回 %d: %s", resp.StatusCode, string(respData))
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
