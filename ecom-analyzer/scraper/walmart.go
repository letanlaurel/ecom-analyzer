package scraper

import (
	"encoding/json"
	"fmt"
	"strings"

	"ecom-analyzer/proxy"

	"github.com/playwright-community/playwright-go"
)

func scrapeWalmart(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	return scrapeWalmartBrowser(pw, keyword, rules)
}

func scrapeWalmartBrowser(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	browser, err := newBrowser(pw)
	if err != nil {
		return nil, err
	}
	defer browser.Close()

	page, err := newStealthPage(browser)
	if err != nil {
		return nil, err
	}

	// 先访问首页建立 cookie
	page.Goto("https://www.walmart.com", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	})
	proxy.RandomSleep(3, 5)

	// 模拟鼠标移动
	page.Mouse().Move(600, 400)
	proxy.RandomSleep(1, 2)

	sortParam := "best_seller"
	switch rules.SortBy {
	case "price-asc-rank":
		sortParam = "price_low"
	case "price-desc-rank":
		sortParam = "price_high"
	case "avg-customer-review":
		sortParam = "rating_high"
	}

	searchURL := fmt.Sprintf("https://www.walmart.com/search?q=%s&sort=%s", urlEncode(keyword), sortParam)
	_, err = page.Goto(searchURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(45000),
	})
	if err != nil {
		return nil, fmt.Errorf("Walmart 导航失败: %w", err)
	}
	proxy.RandomSleep(3, 5)

	// 检查是否被拦截
	html, _ := page.Content()
	if len(html) < 10000 || strings.Contains(html, "Robot or human") || strings.Contains(html, "captcha") {
		return nil, fmt.Errorf("Walmart 触发反爬检测（Cloudflare/PerimeterX）")
	}

	// 滚动触发懒加载
	page.Evaluate(`() => window.scrollTo(0, document.body.scrollHeight / 2)`)
	proxy.RandomSleep(1, 2)
	page.Evaluate(`() => window.scrollTo(0, document.body.scrollHeight)`)
	proxy.RandomSleep(1, 2)

	// 从 __NEXT_DATA__ 提取商品
	result, err := page.Evaluate(`() => {
		try {
			const nd = document.getElementById('__NEXT_DATA__');
			if (!nd) return null;
			const data = JSON.parse(nd.textContent);
			const stacks = data?.props?.pageProps?.initialData?.searchResult?.itemStacks;
			if (!stacks) return null;
			const items = [];
			for (const stack of stacks) {
				for (const item of (stack.items || [])) {
					if (!item || !item.name) continue;
					const price = item.priceInfo?.currentPrice?.price;
					const img = item.imageInfo?.thumbnailUrl || '';
					const url = item.canonicalUrl ? 'https://www.walmart.com' + item.canonicalUrl : '';
					items.push({
						title: item.name,
						price: price ? '$' + price.toFixed(2) : '',
						rating: item.averageRating ? item.averageRating + ' out of 5 stars' : '',
						reviews: item.numberOfReviews ? item.numberOfReviews + ' ratings' : '',
						brand: item.brand || '',
						img: img,
						url: url,
						score: item.averageRating || 0,
						reviewCount: item.numberOfReviews || 0,
					});
				}
			}
			return items;
		} catch(e) { return null; }
	}`)
	if err != nil || result == nil {
		// 降级：从 DOM 提取
		return scrapeWalmartDOM(page, rules)
	}

	rawItems, ok := result.([]interface{})
	if !ok || len(rawItems) == 0 {
		return scrapeWalmartDOM(page, rules)
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
		flt := func(k string) float64 {
			if v, ok := item[k].(float64); ok {
				return v
			}
			return 0
		}
		title := str("title")
		if title == "" {
			continue
		}
		score := flt("score")
		reviewCount := int(flt("reviewCount"))
		if rules.MinRating > 0 && score > 0 && score < rules.MinRating {
			continue
		}
		if rules.MinReviews > 0 && reviewCount < rules.MinReviews {
			continue
		}
		p := Product{
			Source:      "Walmart",
			Title:       title,
			Price:       str("price"),
			Rating:      str("rating"),
			ReviewScore: score,
			ReviewCount: str("reviews"),
			Brand:       str("brand"),
			URL:         str("url"),
		}
		if rules.FetchImage {
			p.ImageURL = str("img")
		}
		products = append(products, p)
	}
	if len(products) == 0 {
		return scrapeWalmartDOM(page, rules)
	}
	return products, nil
}

// scrapeWalmartDOM 从 DOM 直接提取（最后手段）
func scrapeWalmartDOM(page playwright.Page, rules ScrapeRules) ([]Product, error) {
	// 等待商品卡片出现
	page.WaitForSelector(`[data-item-id], [data-testid="list-view"] a[href*="/ip/"]`, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(10000),
	})

	result, err := page.Evaluate(`() => {
		const items = [];
		const seen = new Set();
		const links = document.querySelectorAll('a[href*="/ip/"]');
		for (const a of links) {
			const match = a.href.match(/\/ip\/[^\/]+\/(\d+)/);
			if (!match || seen.has(match[1])) continue;
			seen.add(match[1]);
			let card = a;
			for (let i = 0; i < 8; i++) {
				if (!card.parentElement) break;
				card = card.parentElement;
				if (card.offsetHeight > 200) break;
			}
			let title = a.getAttribute('aria-label') || '';
			if (!title) {
				const h = card.querySelector('[class*="product-title"],[class*="ProductTitle"],span[class*="w_iUH7"]');
				if (h) title = h.innerText.trim();
			}
			if (!title || title.length < 3) continue;
			let price = '';
			const priceEl = card.querySelector('[itemprop="price"],[class*="price-characteristic"],[class*="w_0ZLD"]');
			if (priceEl) price = '$' + (priceEl.getAttribute('content') || priceEl.innerText.trim());
			let img = '';
			const imgEl = card.querySelector('img[src*="walmart"]');
			if (imgEl) img = imgEl.src;
			items.push({ title, price, rating: '', reviews: '', brand: '', img, url: a.href });
			if (items.length >= 48) break;
		}
		return items;
	}`)
	if err != nil || result == nil {
		return nil, fmt.Errorf("Walmart DOM 提取失败")
	}
	rawItems, ok := result.([]interface{})
	if !ok || len(rawItems) == 0 {
		return nil, fmt.Errorf("Walmart 未找到商品")
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
			Source: "Walmart",
			Title:  title,
			Price:  str("price"),
			Brand:  str("brand"),
			URL:    str("url"),
		}
		if rules.FetchImage {
			p.ImageURL = str("img")
		}
		products = append(products, p)
	}
	if len(products) == 0 {
		return nil, fmt.Errorf("Walmart 提取到 0 个商品")
	}
	return products, nil
}

// 保留 scrapeWalmartAPIv2 供内部使用
func scrapeWalmartAPIv2(body []byte, rules ScrapeRules) ([]Product, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("Walmart API 响应解析失败")
	}
	for _, key := range []string{"itemStacks", "searchResult", "paginatedItems"} {
		if data, ok := raw[key]; ok {
			var items []struct {
				Name          string  `json:"name"`
				SalePrice     float64 `json:"salePrice"`
				AverageRating float64 `json:"averageRating"`
				NumReviews    int     `json:"numReviews"`
				ProductURL    string  `json:"productUrl"`
				ThumbnailURL  string  `json:"thumbnailUrl"`
				Brand         string  `json:"brand"`
			}
			if err := json.Unmarshal(data, &items); err == nil && len(items) > 0 {
				limit := rules.MaxItems
				if limit <= 0 {
					limit = 10
				}
				var products []Product
				for _, item := range items {
					if len(products) >= limit {
						break
					}
					if item.Name == "" {
						continue
					}
					price := ""
					if item.SalePrice > 0 {
						price = fmt.Sprintf("$%.2f", item.SalePrice)
					}
					productURL := item.ProductURL
					if productURL != "" && !strings.HasPrefix(productURL, "http") {
						productURL = "https://www.walmart.com" + productURL
					}
					p := Product{
						Source:      "Walmart",
						Title:       item.Name,
						Price:       price,
						ReviewScore: item.AverageRating,
						ReviewCount: fmt.Sprintf("%d ratings", item.NumReviews),
						Brand:       item.Brand,
						URL:         productURL,
						ImageURL:    item.ThumbnailURL,
					}
					products = append(products, p)
				}
				if len(products) > 0 {
					return products, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("Walmart API v2 解析失败")
}


