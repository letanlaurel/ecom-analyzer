package scraper

import (
	"crypto/hmac"
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

func scrapeTaobao(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	appKey := os.Getenv("TAOBAO_APP_KEY")
	appSecret := os.Getenv("TAOBAO_APP_SECRET")
	sessionKey := os.Getenv("TAOBAO_SESSION_KEY") // 淘宝客 session
	if appKey == "" || appSecret == "" {
		return nil, fmt.Errorf("淘宝抓取需要配置 TAOBAO_APP_KEY 和 TAOBAO_APP_SECRET（淘宝开放平台）")
	}
	return scrapeTaobaoAPI(keyword, rules, appKey, appSecret, sessionKey)
}

// scrapeTaobaoAPI 调用淘宝客商品搜索 API（tbk.dg.item.get）
func scrapeTaobaoAPI(keyword string, rules ScrapeRules, appKey, appSecret, sessionKey string) ([]Product, error) {
	limit := rules.MaxItems
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	params := map[string]string{
		"method":       "taobao.tbk.dg.item.get",
		"app_key":      appKey,
		"format":       "json",
		"v":            "2.0",
		"sign_method":  "hmac",
		"timestamp":    time.Now().Format("2006-01-02 15:04:05"),
		"q":            keyword,
		"page_size":    fmt.Sprintf("%d", limit),
		"page_no":      "1",
		"sort":         "tk_total_sales_desc", // 按销量排序
		"platform":     "2",                   // 2=PC
		"fields":       "num_iid,title,pict_url,small_images,reserve_price,zk_final_price,user_type,provcity,item_url,seller_id,volume,nick",
	}
	if sessionKey != "" {
		params["session"] = sessionKey
	}

	// 签名
	sign := taobaoBuildSign(params, appSecret)
	params["sign"] = sign

	// 构建请求
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}

	req, err := http.NewRequest("POST", "https://eco.taobao.com/router/rest", strings.NewReader(form.Encode()))
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
		TbkItemGetResponse struct {
			Result struct {
				ResultList struct {
					MapData []struct {
						Item struct {
							NumIid       int64  `json:"num_iid"`
							Title        string `json:"title"`
							PictURL      string `json:"pict_url"`
							ZkFinalPrice string `json:"zk_final_price"`
							Volume       int    `json:"volume"`
							Nick         string `json:"nick"`
							ItemURL      string `json:"item_url"`
						} `json:"item"`
					} `json:"map_data"`
				} `json:"result_list"`
				TotalResults int `json:"total_results"`
			} `json:"result"`
		} `json:"tbk_dg_item_get_response"`
		Error struct {
			Code    string `json:"code"`
			SubCode string `json:"sub_code"`
			Msg     string `json:"msg"`
			SubMsg  string `json:"sub_msg"`
		} `json:"error_response"`
	}

	if err = json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("淘宝 API 解析失败: %w", err)
	}
	if result.Error.Code != "" {
		return nil, fmt.Errorf("淘宝 API 错误 %s: %s - %s", result.Error.Code, result.Error.Msg, result.Error.SubMsg)
	}

	items := result.TbkItemGetResponse.Result.ResultList.MapData
	if len(items) == 0 {
		return nil, fmt.Errorf("淘宝 API 返回 0 个商品")
	}

	var products []Product
	for _, d := range items {
		item := d.Item
		if item.Title == "" {
			continue
		}
		price := ""
		if item.ZkFinalPrice != "" {
			price = "¥" + item.ZkFinalPrice
		}
		imgURL := item.PictURL
		if imgURL != "" && strings.HasPrefix(imgURL, "//") {
			imgURL = "https:" + imgURL
		}
		productURL := item.ItemURL
		if productURL == "" && item.NumIid > 0 {
			productURL = fmt.Sprintf("https://item.taobao.com/item.htm?id=%d", item.NumIid)
		}
		p := Product{
			Source:      "淘宝",
			Title:       item.Title,
			Price:       price,
			ReviewCount: fmt.Sprintf("%d 月销", item.Volume),
			Brand:       item.Nick,
			URL:         productURL,
		}
		if rules.FetchImage {
			p.ImageURL = imgURL
		}
		products = append(products, p)
	}
	return products, nil
}

// taobaoBuildSign 淘宝 API HMAC-MD5 签名
func taobaoBuildSign(params map[string]string, secret string) string {
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

	mac := hmac.New(md5.New, []byte(secret))
	mac.Write([]byte(sb.String()))
	return strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))
}

// 避免 playwright 未使用报错
var _ = (*playwright.Playwright)(nil)
