package scraper

import (
	"fmt"
	"strings"

	"ecom-analyzer/proxy"

	"github.com/playwright-community/playwright-go"
)

func scrapeAliExpress(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	return scrapeAliExpressBrowser(pw, keyword, rules)
}

func scrapeAliExpressBrowser(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	browser, err := newBrowser(pw)
	if err != nil {
		return nil, err
	}
	defer browser.Close()

	page, err := newStealthPage(browser)
	if err != nil {
		return nil, err
	}

	// 先访问首页建立 cookie，避免直接跳搜索页被识别为机器人
	page.Goto("https://www.aliexpress.com", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	})
	proxy.RandomSleep(3, 5)

	// 检查是否被重定向到登录页
	currentURL := page.URL()
	if strings.Contains(currentURL, "login") || strings.Contains(currentURL, "sign") {
		return nil, fmt.Errorf("AliExpress 需要登录，无法抓取")
	}

	// 模拟鼠标移动
	page.Mouse().Move(500, 300)
	proxy.RandomSleep(1, 2)

	sortParam := "default"
	switch rules.SortBy {
	case "price-asc-rank":
		sortParam = "price_asc"
	case "price-desc-rank":
		sortParam = "price_desc"
	case "avg-customer-review":
		sortParam = "rating_desc"
	}

	searchURL := fmt.Sprintf(
		"https://www.aliexpress.com/wholesale?SearchText=%s&SortType=%s",
		urlEncode(keyword), sortParam,
	)
	_, err = page.Goto(searchURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(45000),
	})
	if err != nil {
		return nil, fmt.Errorf("AliExpress 导航失败: %w", err)
	}
	proxy.RandomSleep(3, 5)

	// 检查是否被重定向到登录页
	currentURL = page.URL()
	if strings.Contains(currentURL, "login") || strings.Contains(currentURL, "sign") {
		return nil, fmt.Errorf("AliExpress 重定向到登录页，无法抓取")
	}

	html, _ := page.Content()
	if len(html) < 5000 {
		return nil, fmt.Errorf("AliExpress 页面内容异常（可能触发反爬）")
	}

	// 滚动触发懒加载
	page.Evaluate(`() => window.scrollTo(0, document.body.scrollHeight / 2)`)
	proxy.RandomSleep(1, 2)
	page.Evaluate(`() => window.scrollTo(0, document.body.scrollHeight)`)
	proxy.RandomSleep(1, 2)

	// 等待商品卡片出现
	page.WaitForSelector(`a[href*="/item/"]`, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(10000),
	})

	// 从 DOM 提取商品
	result, err := page.Evaluate(`() => {
		const items = [];
		const seen = new Set();
		const links = document.querySelectorAll('a[href*="/item/"]');
		for (const a of links) {
			const href = a.href.startsWith('//') ? 'https:' + a.href : a.href;
			const match = href.match(/\/item\/(\d+)\.html/);
			if (!match || seen.has(match[1])) continue;
			seen.add(match[1]);

			// 找商品卡片容器
			let card = a;
			for (let i = 0; i < 8; i++) {
				if (!card.parentElement) break;
				card = card.parentElement;
				if (card.offsetHeight > 200 && card.offsetWidth > 100) break;
			}

			// 标题
			let title = '';
			const titleEl = card.querySelector('h1,h3,[class*="title"],[class*="Title"],[class*="name"],[class*="Name"]');
			if (titleEl) title = titleEl.innerText.trim();
			if (!title) title = a.title || a.getAttribute('aria-label') || '';
			if (!title) {
				// 从链接文本提取
				const spans = a.querySelectorAll('span');
				for (const s of spans) {
					const t = s.innerText.trim();
					if (t.length > 10) { title = t; break; }
				}
			}
			if (!title || title.length < 3) continue;

			// 价格
			let price = '';
			const priceEl = card.querySelector('[class*="price"],[class*="Price"],[class*="cost"],[class*="Cost"]');
			if (priceEl) {
				price = priceEl.innerText.trim().split('\n')[0].replace(/\s+/g, ' ');
			}

			// 评分
			let rating = '';
			const ratingEl = card.querySelector('[class*="star"],[class*="Star"],[class*="rating"],[class*="Rating"]');
			if (ratingEl) rating = ratingEl.innerText.trim().split('\n')[0];

			// 销量/评论
			let sold = '';
			const soldEl = card.querySelector('[class*="sold"],[class*="Sold"],[class*="order"],[class*="review"]');
			if (soldEl) sold = soldEl.innerText.trim();

			// 图片
			let img = '';
			const imgEl = card.querySelector('img');
			if (imgEl) {
				img = imgEl.src || imgEl.dataset.src || imgEl.dataset.lazySrc || '';
				if (img.startsWith('//')) img = 'https:' + img;
			}

			// 店铺名
			let store = '';
			const storeEl = card.querySelector('[class*="store"],[class*="Store"],[class*="shop"],[class*="Shop"]');
			if (storeEl) store = storeEl.innerText.trim();

			items.push({ title, price, rating, sold, img, store, url: href });
			if (items.length >= 60) break;
		}
		return items.length > 0 ? items : null;
	}`)
	if err != nil || result == nil {
		return nil, fmt.Errorf("AliExpress 提取失败（可能需要登录）")
	}

	rawItems, ok := result.([]interface{})
	if !ok || len(rawItems) == 0 {
		return nil, fmt.Errorf("AliExpress 未找到商品")
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
			Source:      "AliExpress",
			Title:       title,
			Price:       str("price"),
			Rating:      str("rating"),
			ReviewCount: str("sold"),
			Brand:       str("store"),
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
		return nil, fmt.Errorf("AliExpress 提取到 0 个有效商品")
	}
	return products, nil
}
