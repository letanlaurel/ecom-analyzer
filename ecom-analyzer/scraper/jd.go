package scraper

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
)

func scrapeJD(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	appKey := os.Getenv("JD_APP_KEY")
	appSecret := os.Getenv("JD_APP_SECRET")
	if appKey == "" || appSecret == "" {
		return nil, fmt.Errorf("京东抓取需要配置 JD_APP_KEY 和 JD_APP_SECRET（京东联盟开放平台）")
	}
	return scrapeJDAPI(keyword, rules, appKey, appSecret)
}

// scrapeJDAPI 调用京东联盟商品查询 API（jd.union.open.goods.query）
func scrapeJDAPI(keyword string, rules ScrapeRules, appKey, appSecret string) ([]Product, error) {
	limit := rules.MaxItems
	if limit <= 0 {
		limit = 10
	}
	if limit > 30 {
		limit = 30
	}

	// 构建业务参数
	goodsReq := map[string]interface{}{
		"keyword":  keyword,
		"pageSize": limit,
		"pageIndex": 1,
		"sortName": "inOrderCount30DaysSku", // 30天销量排序
		"sort":     "desc",
	}
	goodsReqJSON, _ := json.Marshal(goodsReq)

	params := map[string]string{
		"method":      "jd.union.open.goods.query",
		"app_key":     appKey,
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"format":      "json",
		"v":           "1.0",
		"sign_method": "md5",
		"param_json":  string(goodsReqJSON),
	}

	params["sign"] = jdBuildSign(params, appSecret)

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}

	req, err := http.NewRequest("POST", "https://router.jd.com/api", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		QueryResult struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    struct {
				Data []struct {
					SkuName    string `json:"skuName"`
					SkuID      int64  `json:"skuId"`
					ImageURL   string `json:"imageUrl"`
					Price      struct {
						Price    string `json:"price"`
						LowestPrice string `json:"lowestPrice"`
					} `json:"priceInfo"`
					CommissionInfo struct {
						Commission     float64 `json:"commission"`
						CommissionRate float64 `json:"commissionRate"`
					} `json:"commissionInfo"`
					ShopInfo struct {
						ShopName string `json:"shopName"`
					} `json:"shopInfo"`
					InOrderCount30Days int `json:"inOrderCount30Days"`
					CommentInfo struct {
						CommentCount string `json:"commentCount"`
					} `json:"commentInfo"`
					MaterialURL string `json:"materialUrl"`
				} `json:"data"`
				TotalCount int `json:"totalCount"`
			} `json:"data"`
		} `json:"queryResult"`
	}

	if err = json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("京东 API 解析失败: %w\n原始响应: %s", err, string(body))
	}
	if result.QueryResult.Code != 200 {
		return nil, fmt.Errorf("京东 API 错误 %d: %s", result.QueryResult.Code, result.QueryResult.Message)
	}

	items := result.QueryResult.Data.Data
	if len(items) == 0 {
		return nil, fmt.Errorf("京东 API 返回 0 个商品")
	}

	var products []Product
	for _, item := range items {
		if item.SkuName == "" {
			continue
		}
		price := item.Price.Price
		if price == "" {
			price = item.Price.LowestPrice
		}
		if price != "" {
			price = "¥" + price
		}
		imgURL := item.ImageURL
		if imgURL != "" && strings.HasPrefix(imgURL, "//") {
			imgURL = "https:" + imgURL
		}
		productURL := item.MaterialURL
		if productURL == "" && item.SkuID > 0 {
			productURL = fmt.Sprintf("https://item.jd.com/%d.html", item.SkuID)
		}
		reviews := item.CommentInfo.CommentCount
		if reviews != "" {
			reviews += " 评价"
		}
		if item.InOrderCount30Days > 0 {
			if reviews != "" {
				reviews += fmt.Sprintf(" · %d 月销", item.InOrderCount30Days)
			} else {
				reviews = fmt.Sprintf("%d 月销", item.InOrderCount30Days)
			}
		}
		p := Product{
			Source:      "京东",
			Title:       item.SkuName,
			Price:       price,
			ReviewCount: reviews,
			Brand:       item.ShopInfo.ShopName,
			URL:         productURL,
		}
		if rules.FetchImage {
			p.ImageURL = imgURL
		}
		products = append(products, p)
	}
	return products, nil
}

// jdBuildSign 京东 API MD5 签名
func jdBuildSign(params map[string]string, secret string) string {
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
