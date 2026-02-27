package scraper

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
)

func scrapePinduoduo(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	clientID := os.Getenv("PDD_CLIENT_ID")
	clientSecret := os.Getenv("PDD_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("拼多多抓取需要配置 PDD_CLIENT_ID 和 PDD_CLIENT_SECRET（拼多多开放平台）")
	}
	return scrapePDDAPI(keyword, rules, clientID, clientSecret)
}

// scrapePDDAPI 调用拼多多多多客商品搜索 API（pdd.ddk.goods.search）
func scrapePDDAPI(keyword string, rules ScrapeRules, clientID, clientSecret string) ([]Product, error) {
	limit := rules.MaxItems
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	sortType := 0 // 0=综合排序
	switch rules.SortBy {
	case "price-asc-rank":
		sortType = 2
	case "price-desc-rank":
		sortType = 3
	case "avg-customer-review":
		sortType = 1 // 销量
	}

	params := map[string]string{
		"type":           "pdd.ddk.goods.search",
		"client_id":      clientID,
		"timestamp":      fmt.Sprintf("%d", time.Now().Unix()),
		"data_type":      "JSON",
		"version":        "V1",
		"keyword":        keyword,
		"page":           "1",
		"page_size":      fmt.Sprintf("%d", limit),
		"sort_type":      fmt.Sprintf("%d", sortType),
		"with_coupon":    "0",
	}

	params["sign"] = pddBuildSign(params, clientSecret)

	// 构建 JSON body
	body, _ := json.Marshal(params)

	req, err := http.NewRequest("POST", "https://gw-api.pinduoduo.com/api/router", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		GoodsSearchResponse struct {
			GoodsList []struct {
				GoodsName      string  `json:"goods_name"`
				GoodsID        int64   `json:"goods_id"`
				GoodsImageURL  string  `json:"goods_image_url"`
				MinGroupPrice  int64   `json:"min_group_price"`  // 单位：分
				MinNormalPrice int64   `json:"min_normal_price"` // 单位：分
				SalesTip       string  `json:"sales_tip"`
				MallName       string  `json:"mall_name"`
				GoodsURL       string  `json:"goods_url"`
				GoodsDesc      string  `json:"goods_desc"`
			} `json:"goods_list"`
			TotalCount int `json:"total_count"`
		} `json:"goods_search_response"`
		ErrorResponse *struct {
			ErrorMsg  string `json:"error_msg"`
			SubMsg    string `json:"sub_msg"`
			ErrorCode int    `json:"error_code"`
		} `json:"error_response"`
	}

	if err = json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("拼多多 API 解析失败: %w\n原始响应: %s", err, string(respBody))
	}
	if result.ErrorResponse != nil {
		return nil, fmt.Errorf("拼多多 API 错误 %d: %s - %s",
			result.ErrorResponse.ErrorCode,
			result.ErrorResponse.ErrorMsg,
			result.ErrorResponse.SubMsg,
		)
	}

	items := result.GoodsSearchResponse.GoodsList
	if len(items) == 0 {
		return nil, fmt.Errorf("拼多多 API 返回 0 个商品")
	}

	var products []Product
	for _, item := range items {
		if item.GoodsName == "" {
			continue
		}
		// 价格单位是分，转换为元
		price := ""
		if item.MinGroupPrice > 0 {
			price = fmt.Sprintf("¥%.2f", float64(item.MinGroupPrice)/100)
		} else if item.MinNormalPrice > 0 {
			price = fmt.Sprintf("¥%.2f", float64(item.MinNormalPrice)/100)
		}
		imgURL := item.GoodsImageURL
		if imgURL != "" && strings.HasPrefix(imgURL, "//") {
			imgURL = "https:" + imgURL
		}
		productURL := item.GoodsURL
		if productURL == "" && item.GoodsID > 0 {
			productURL = fmt.Sprintf("https://mobile.yangkeduo.com/goods.html?goods_id=%d", item.GoodsID)
		}
		p := Product{
			Source:      "拼多多",
			Title:       item.GoodsName,
			Price:       price,
			ReviewCount: item.SalesTip,
			Brand:       item.MallName,
			Description: item.GoodsDesc,
			URL:         productURL,
		}
		if rules.FetchImage {
			p.ImageURL = imgURL
		}
		products = append(products, p)
	}
	return products, nil
}

// pddBuildSign 拼多多 API MD5 签名
func pddBuildSign(params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString(secret)
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString(params[k])
	}
	sb.WriteString(secret)

	h := md5.Sum([]byte(sb.String()))
	return strings.ToUpper(hex.EncodeToString(h[:]))
}

// 避免 playwright 未使用报错
var _ = (*playwright.Playwright)(nil)
