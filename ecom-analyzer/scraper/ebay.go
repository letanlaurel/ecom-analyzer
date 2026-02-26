package scraper

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
)

// scrapeEbay 优先用官方 Browse API，无 token 时降级到无头浏览器
func scrapeEbay(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	if os.Getenv("EBAY_APP_ID") != "" {
		products, err := scrapeEbayAPI(keyword, rules)
		if err == nil {
			return products, nil
		}
	}
	return scrapeEbayBrowser(pw, keyword, rules)
}

// scrapeEbayAPI 调用 eBay Browse API
func scrapeEbayAPI(keyword string, rules ScrapeRules) ([]Product, error) {
	appID := os.Getenv("EBAY_APP_ID")
	limit := rules.MaxItems
	if limit <= 0 {
		limit = 10
	}

	token, err := getEbayToken(appID, os.Getenv("EBAY_CERT_ID"))
	if err != nil {
		return nil, fmt.Errorf("eBay token 获取失败: %w", err)
	}

	apiURL := fmt.Sprintf(
		"https://api.ebay.com/buy/browse/v1/item_summary/search?q=%s&limit=%d&sort=BEST_MATCH",
		url.QueryEscape(keyword), limit,
	)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-EBAY-C-MARKETPLACE-ID", "EBAY_US")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("eBay API %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ItemSummaries []struct {
			Title      string `json:"title"`
			ItemWebURL string `json:"itemWebUrl"`
			Image      struct {
				ImageURL string `json:"imageUrl"`
			} `json:"image"`
			Price struct {
				Value    string `json:"value"`
				Currency string `json:"currency"`
			} `json:"price"`
			Seller struct {
				FeedbackScore      int    `json:"feedbackScore"`
				FeedbackPercentage string `json:"feedbackPercentage"`
			} `json:"seller"`
			Condition       string `json:"condition"`
			ShippingOptions []struct {
				ShippingCost struct {
					Value string `json:"value"`
				} `json:"shippingCost"`
			} `json:"shippingOptions"`
		} `json:"itemSummaries"`
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	var products []Product
	for _, item := range result.ItemSummaries {
		price := item.Price.Value
		if item.Price.Currency != "" {
			price = item.Price.Currency + " " + price
		}
		shipping := ""
		if len(item.ShippingOptions) > 0 {
			v := item.ShippingOptions[0].ShippingCost.Value
			if v == "0.00" {
				shipping = "免运费"
			} else if v != "" {
				shipping = "运费 $" + v
			}
		}
		desc := item.Condition
		if shipping != "" {
			desc = desc + " · " + shipping
		}
		p := Product{
			Source:      "eBay",
			Title:       item.Title,
			Price:       price,
			Rating:      fmt.Sprintf("卖家好评率 %s%%", item.Seller.FeedbackPercentage),
			ReviewCount: fmt.Sprintf("%d 条反馈", item.Seller.FeedbackScore),
			Description: desc,
			URL:         item.ItemWebURL,
		}
		if rules.FetchImage {
			p.ImageURL = item.Image.ImageURL
		}
		products = append(products, p)
	}
	return products, nil
}

func getEbayToken(appID, certID string) (string, error) {
	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("scope", "https://api.ebay.com/oauth/api_scope")

	req, _ := http.NewRequest("POST", "https://api.ebay.com/identity/v1/oauth2/token",
		strings.NewReader(data.Encode()))
	req.SetBasicAuth(appID, certID)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error_description"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err = json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf("%s", result.Error)
	}
	return result.AccessToken, nil
}

// scrapeEbayBrowser 无头浏览器方案
func scrapeEbayBrowser(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	browser, err := newBrowser(pw)
	if err != nil {
		return nil, err
	}
	defer browser.Close()

	page, err := newPage(browser)
	if err != nil {
		return nil, err
	}

	// 先访问首页建立 cookie
	page.Goto("https://www.ebay.com", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(20000),
	})
	waitRandom(page, 1, 2)

	searchURL := fmt.Sprintf("https://www.ebay.com/sch/i.html?_nkw=%s&_sop=12&_ipg=48", urlEncode(keyword))
	if _, err = page.Goto(searchURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		return nil, fmt.Errorf("eBay 导航失败: %w", err)
	}

	// 等待商品卡片出现（eBay 用 JS 动态渲染，新版 class 为 li.s-card）
	page.WaitForSelector(`li.s-card, li.s-item`, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(15000),
	})
	waitRandom(page, 1, 2)

	// 滚动页面触发懒加载
	page.Evaluate(`() => { window.scrollTo(0, document.body.scrollHeight / 2); }`)
	waitRandom(page, 1, 2)
	page.Evaluate(`() => { window.scrollTo(0, document.body.scrollHeight); }`)
	waitRandom(page, 1, 2)
	page.Evaluate(`() => { window.scrollTo(0, 0); }`)
	waitRandom(page, 0, 1)

	// 新版 eBay 用 li.s-card，旧版用 li.s-item
	cards, err := page.QuerySelectorAll(`li.s-card`)
	if err != nil || len(cards) == 0 {
		cards, err = page.QuerySelectorAll(`li.s-item`)
		if err != nil || len(cards) == 0 {
			return nil, fmt.Errorf("eBay 未找到商品卡片")
		}
	}

	maxItems := rules.MaxItems
	if maxItems <= 0 {
		maxItems = 10
	}
	var products []Product
	for i := 0; i < len(cards) && len(products) < maxItems; i++ {
		card := cards[i]
		var p Product

		// 新版标题选择器
		titleSels := []string{
			`.s-card__title`,
			`.s-item__title`,
			`h3.s-card__title`,
		}
		for _, sel := range titleSels {
			if el, err := card.QuerySelector(sel); err == nil && el != nil {
				if t, _ := el.InnerText(); t != "" && t != "Shop on eBay" {
					p.Title = strings.TrimSpace(t)
					break
				}
			}
		}
		if p.Title == "" {
			continue
		}

		// 价格
		priceSels := []string{`.s-card__price`, `.s-item__price`}
		for _, sel := range priceSels {
			if el, err := card.QuerySelector(sel); err == nil && el != nil {
				if t, _ := el.InnerText(); t != "" {
					p.Price = strings.TrimSpace(t)
					break
				}
			}
		}

		// 评分
		if el, err := card.QuerySelector(`[aria-label*="out of 5"]`); err == nil && el != nil {
			if aria, _ := el.GetAttribute("aria-label"); aria != "" {
				p.Rating = aria
				p.ReviewScore = parseRatingScore(aria)
			}
		}

		// 评论数
		reviewSels := []string{`.s-card__reviews-count`, `.s-item__reviews-count span`}
		for _, sel := range reviewSels {
			if el, err := card.QuerySelector(sel); err == nil && el != nil {
				if t, _ := el.InnerText(); t != "" {
					p.ReviewCount = strings.TrimSpace(t)
					break
				}
			}
		}

		// URL
		linkSels := []string{`a.s-card__link`, `a.s-item__link`}
		for _, sel := range linkSels {
			if el, err := card.QuerySelector(sel); err == nil && el != nil {
				if href, _ := el.GetAttribute("href"); href != "" {
					p.URL = href
					break
				}
			}
		}

		// 图片
		if rules.FetchImage {
			imgSels := []string{`img.s-card__image`, `img.s-item__image-img`}
			for _, sel := range imgSels {
				if el, err := card.QuerySelector(sel); err == nil && el != nil {
					for _, attr := range []string{"src", "data-src"} {
						if src, _ := el.GetAttribute(attr); src != "" && !strings.Contains(src, "gif") {
							p.ImageURL = src
							break
						}
					}
					if p.ImageURL != "" {
						break
					}
				}
			}
		}

		if rules.MinRating > 0 && p.ReviewScore > 0 && p.ReviewScore < rules.MinRating {
			continue
		}
		p.Source = "eBay"
		products = append(products, p)
	}
	return products, nil
}
