package scraper

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"ecom-analyzer/proxy"

	"github.com/playwright-community/playwright-go"
)

func scrapeEtsy(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	if key := os.Getenv("ETSY_API_KEY"); key != "" {
		products, err := scrapeEtsyAPI(keyword, rules)
		if err == nil {
			return products, nil
		}
	}
	return scrapeEtsyBrowser(pw, keyword, rules)
}

func scrapeEtsyAPI(keyword string, rules ScrapeRules) ([]Product, error) {
	apiKey := os.Getenv("ETSY_API_KEY")
	limit := rules.MaxItems
	if limit <= 0 {
		limit = 10
	}

	apiURL := fmt.Sprintf(
		"https://openapi.etsy.com/v3/application/listings/active?keywords=%s&limit=%d&sort_on=score&includes=Images,Shop",
		url.QueryEscape(keyword), limit,
	)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("x-api-key", apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Etsy API %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Results []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Price       struct {
				Amount       int    `json:"amount"`
				Divisor      int    `json:"divisor"`
				CurrencyCode string `json:"currency_code"`
			} `json:"price"`
			NumFavorers int    `json:"num_favorers"`
			Views       int    `json:"views"`
			URL         string `json:"url"`
			Images      []struct {
				URL570xN string `json:"url_570xN"`
			} `json:"images"`
			Shop *struct {
				ShopName string `json:"shop_name"`
			} `json:"shop"`
		} `json:"results"`
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	var products []Product
	for _, item := range result.Results {
		price := ""
		if item.Price.Divisor > 0 {
			val := float64(item.Price.Amount) / float64(item.Price.Divisor)
			price = item.Price.CurrencyCode + " " + strconv.FormatFloat(val, 'f', 2, 64)
		}
		desc := item.Description
		if len(desc) > 500 {
			desc = desc[:500] + "..."
		}
		brand := ""
		if item.Shop != nil {
			brand = item.Shop.ShopName
		}
		p := Product{
			Source:      "Etsy",
			Title:       item.Title,
			Price:       price,
			ReviewCount: fmt.Sprintf("%d 收藏 · %d 浏览", item.NumFavorers, item.Views),
			Description: desc,
			Brand:       brand,
			URL:         item.URL,
		}
		if rules.FetchImage && len(item.Images) > 0 {
			p.ImageURL = item.Images[0].URL570xN
		}
		products = append(products, p)
	}
	return products, nil
}

// scrapeEtsyBrowser Etsy 有 DataDome 反爬，需要 stealth + 模拟人工操作
func scrapeEtsyBrowser(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	browser, err := newBrowser(pw)
	if err != nil {
		return nil, err
	}
	defer browser.Close()

	page, err := newStealthPage(browser)
	if err != nil {
		return nil, err
	}

	// 先访问首页，等待 cookie 建立
	page.Goto("https://www.etsy.com", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	})
	proxy.RandomSleep(4, 6)

	// 检查首页是否被拦截
	homeHTML, _ := page.Content()
	if len(homeHTML) < 5000 {
		return nil, fmt.Errorf("Etsy 首页触发反爬检测（DataDome CAPTCHA）")
	}

	// 模拟鼠标移动和滚动
	page.Mouse().Move(400, 300)
	proxy.RandomSleep(1, 2)
	page.Evaluate(`() => window.scrollTo(0, 200)`)
	proxy.RandomSleep(1, 2)
	page.Mouse().Move(600, 400)
	proxy.RandomSleep(1, 2)

	// 用搜索框搜索（更像人工操作）
	searchSelectors := []string{
		`input[name="search_query"]`,
		`#global-enhancements-search-query`,
		`input[type="search"]`,
		`[data-search-query]`,
	}
	var searchBox playwright.ElementHandle
	for _, sel := range searchSelectors {
		if el, err := page.QuerySelector(sel); err == nil && el != nil {
			searchBox = el
			break
		}
	}

	if searchBox != nil {
		searchBox.Click()
		proxy.RandomSleep(1, 2)
		searchBox.Type(keyword, playwright.ElementHandleTypeOptions{Delay: playwright.Float(100)})
		proxy.RandomSleep(1, 2)
		page.Keyboard().Press("Enter")
		proxy.RandomSleep(4, 6)
	} else {
		// 降级：直接访问搜索 URL
		searchURL := fmt.Sprintf("https://www.etsy.com/search?q=%s&order=most_relevant", urlEncode(keyword))
		page.Goto(searchURL, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
			Timeout:   playwright.Float(40000),
		})
		proxy.RandomSleep(4, 6)
	}

	// 检查是否被拦截
	html, _ := page.Content()
	if len(html) < 5000 {
		return nil, fmt.Errorf("Etsy 触发反爬检测（DataDome CAPTCHA）")
	}

	// 用 JS 提取商品
	result, err := page.Evaluate(`() => {
		const items = [];
		const links = document.querySelectorAll('a[href*="/listing/"]');
		const seen = new Set();
		for (const a of links) {
			const match = a.href.match(/\/listing\/(\d+)\//);
			if (!match || seen.has(match[1])) continue;
			seen.add(match[1]);

			let card = a;
			for (let i = 0; i < 8; i++) {
				if (!card.parentElement) break;
				card = card.parentElement;
				if (card.tagName === 'LI' || card.offsetHeight > 250) break;
			}

			let title = a.title || '';
			if (!title) {
				const h = card.querySelector('h3,h2,[class*="title"],[class*="Title"]');
				if (h) title = h.innerText.trim();
			}
			if (!title) title = a.innerText.trim().split('\n')[0];
			if (!title || title.length < 3) continue;

			let price = '';
			const priceEl = card.querySelector('[class*="currency"],[class*="price"],[class*="Price"]');
			if (priceEl) price = priceEl.innerText.trim().split('\n')[0];

			let rating = '';
			const ratingEl = card.querySelector('[aria-label*="star"], [class*="rating"]');
			if (ratingEl) rating = ratingEl.getAttribute('aria-label') || ratingEl.innerText.trim();

			let reviews = '';
			const reviewEl = card.querySelector('[class*="count"],[class*="review"]');
			if (reviewEl) reviews = reviewEl.innerText.trim();

			let img = '';
			const imgEl = card.querySelector('img');
			if (imgEl) img = imgEl.src || imgEl.dataset.src || '';

			let shop = '';
			const shopEl = card.querySelector('[class*="shop"],[class*="Shop"]');
			if (shopEl) shop = shopEl.innerText.trim();

			items.push({ title, price, rating, reviews, img, shop, url: a.href });
			if (items.length >= 48) break;
		}
		return items;
	}`)
	if err != nil {
		return nil, fmt.Errorf("Etsy JS 提取失败: %w", err)
	}

	rawItems, ok := result.([]interface{})
	if !ok || len(rawItems) == 0 {
		return nil, fmt.Errorf("Etsy 未找到商品")
	}

	maxItems := rules.MaxItems
	if maxItems <= 0 {
		maxItems = 10
	}

	var products []Product
	for _, raw := range rawItems {
		if len(products) >= maxItems {
			break
		}
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		str := func(k string) string {
			if v, ok := item[k].(string); ok {
				return v
			}
			return ""
		}
		title := str("title")
		if title == "" {
			continue
		}
		p := Product{
			Source:      "Etsy",
			Title:       title,
			Price:       str("price"),
			Rating:      str("rating"),
			ReviewCount: str("reviews"),
			Brand:       str("shop"),
			URL:         str("url"),
		}
		p.ReviewScore = parseRatingScore(p.Rating)
		if rules.FetchImage {
			p.ImageURL = str("img")
		}
		if rules.MinRating > 0 && p.ReviewScore > 0 && p.ReviewScore < rules.MinRating {
			continue
		}
		products = append(products, p)
	}
	if len(products) == 0 {
		return nil, fmt.Errorf("Etsy 提取到 0 个有效商品")
	}
	return products, nil
}
