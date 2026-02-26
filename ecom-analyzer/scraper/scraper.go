package scraper

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"ecom-analyzer/proxy"

	"github.com/playwright-community/playwright-go"
)

// Product 抓取到的商品结构
type Product struct {
	Source      string `json:"source"`
	Title       string `json:"title"`
	Price       string `json:"price"`
	Rating      string `json:"rating"`
	ReviewCount string `json:"review_count"`
	ReviewScore float64 `json:"review_score"` // 纯数字评分，方便过滤
	Description string `json:"description"`
	ASIN        string `json:"asin,omitempty"`
	Brand       string `json:"brand,omitempty"`
	IsPrime     bool   `json:"is_prime,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	URL         string `json:"url"`
}

// ScrapeRules 抓取规则，由前端传入
type ScrapeRules struct {
	MaxItems        int     `json:"max_items"`         // 最多抓取数量，默认 10
	MinRating       float64 `json:"min_rating"`        // 最低评分过滤，0 = 不过滤
	MinReviews      int     `json:"min_reviews"`       // 最少评论数过滤，0 = 不过滤
	FetchDetail     bool    `json:"fetch_detail"`      // 是否抓取详情页描述
	FetchImage      bool    `json:"fetch_image"`       // 是否抓取商品图片
	PrimeOnly       bool    `json:"prime_only"`        // 仅抓取 Prime 商品
	SortBy          string  `json:"sort_by"`           // amazon: review-rank | price-asc-rank | date-desc-rank
}

// ScrapeAll 并发抓取 Amazon + TikTok Shop，返回合并结果
func ScrapeAll(keyword string) ([]Product, error) {
	return ScrapeWithPlatforms(keyword, []string{"amazon", "tiktok"}, nil, DefaultRules())
}

// DefaultRules 返回默认抓取规则
func DefaultRules() ScrapeRules {
	return ScrapeRules{
		MaxItems:    10,
		FetchDetail: true,
		FetchImage:  true,
		SortBy:      "review-rank",
	}
}

// ScrapeWithPlatforms 按指定平台并发抓取，progress 回调用于上报进度
func ScrapeWithPlatforms(keyword string, platforms []string, progress func(string), rules ScrapeRules) ([]Product, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("playwright 启动失败: %w", err)
	}
	defer pw.Stop()

	if progress != nil {
		progress("浏览器已启动，开始抓取...")
	}

	// 根据 platforms 过滤
	platformSet := map[string]bool{}
	for _, p := range platforms {
		platformSet[strings.ToLower(p)] = true
	}
	// 默认全抓
	if len(platformSet) == 0 {
		platformSet["amazon"] = true
		platformSet["tiktok"] = true
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		products []Product
	)

	allSources := []struct {
		key     string
		name    string
		scraper func(*playwright.Playwright, string, ScrapeRules) ([]Product, error)
	}{
		{"amazon", "Amazon", scrapeAmazon},
		{"tiktok", "TikTokShop", scrapeTikTokShop},
	}

	for _, s := range allSources {
		if !platformSet[s.key] {
			continue
		}
		wg.Add(1)
		go func(key, name string, fn func(*playwright.Playwright, string, ScrapeRules) ([]Product, error)) {
			defer wg.Done()
			if progress != nil {
				progress(fmt.Sprintf("[%s] 正在抓取...", name))
			}
			items, err := fn(pw, keyword, rules)
			if err != nil {
				log.Printf("[%s] 抓取失败: %v", name, err)
				if progress != nil {
					progress(fmt.Sprintf("[%s] 抓取失败: %v", name, err))
				}
				return
			}
			if progress != nil {
				progress(fmt.Sprintf("[%s] 抓取完成，获得 %d 个商品", name, len(items)))
			}
			mu.Lock()
			products = append(products, items...)
			mu.Unlock()
		}(s.key, s.name, s.scraper)
	}

	wg.Wait()
	return products, nil
}

// newBrowser 创建带代理和 UA 的浏览器实例
func newBrowser(pw *playwright.Playwright) (playwright.Browser, error) {
	opts := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	}
	proxyURL := proxy.GetRandomProxy()
	if proxyURL != "" {
		opts.Proxy = &playwright.Proxy{
			Server: proxyURL,
		}
	}
	return pw.Chromium.Launch(opts)
}

// newPage 创建带随机 UA 的页面
func newPage(browser playwright.Browser) (playwright.Page, error) {
	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: playwright.String(proxy.GetRandomUserAgent()),
		ExtraHttpHeaders: map[string]string{
			"Accept-Language": "en-US,en;q=0.9",
		},
	})
	if err != nil {
		return nil, err
	}
	return ctx.NewPage()
}

// scrapeAmazon 抓取 Amazon 搜索结果
func scrapeAmazon(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	browser, err := newBrowser(pw)
	if err != nil {
		return nil, err
	}
	defer browser.Close()

	page, err := newPage(browser)
	if err != nil {
		return nil, err
	}

	sortBy := rules.SortBy
	if sortBy == "" {
		sortBy = "review-rank"
	}
	url := fmt.Sprintf("https://www.amazon.com/s?k=%s&sort=%s", urlEncode(keyword), sortBy)
	if _, err = page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		return nil, fmt.Errorf("Amazon 导航失败: %w", err)
	}

	proxy.RandomSleep(2, 4)

	cards, err := page.QuerySelectorAll(`div[data-component-type="s-search-result"]`)
	if err != nil || len(cards) == 0 {
		return nil, fmt.Errorf("Amazon 未找到商品卡片")
	}

	maxItems := rules.MaxItems
	if maxItems <= 0 {
		maxItems = 10
	}
	limit := maxItems
	if len(cards) < limit {
		limit = len(cards)
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		products []Product
	)

	sem := make(chan struct{}, 3)
	for i := 0; i < limit; i++ {
		wg.Add(1)
		card := cards[i]
		go func(c playwright.ElementHandle) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			p := extractAmazonCard(c, rules)
			if p.Title == "" {
				return
			}
			// 评分过滤
			if rules.MinRating > 0 && p.ReviewScore < rules.MinRating {
				return
			}
			// Prime 过滤
			if rules.PrimeOnly && !p.IsPrime {
				return
			}
			p.Source = "Amazon"

			if rules.FetchDetail && p.URL != "" {
				p.Description = fetchAmazonDescription(browser, p.URL)
			}

			mu.Lock()
			products = append(products, p)
			mu.Unlock()
		}(card)
	}
	wg.Wait()

	// 评论数过滤（需要解析数字）
	if rules.MinReviews > 0 {
		filtered := products[:0]
		for _, p := range products {
			if parseReviewCount(p.ReviewCount) >= rules.MinReviews {
				filtered = append(filtered, p)
			}
		}
		products = filtered
	}

	return products, nil
}

func extractAmazonCard(card playwright.ElementHandle, rules ScrapeRules) Product {
	var p Product

	// 标题
	titleSelectors := []string{`h2 span`, `[data-cy="title-recipe"] span`, `.a-size-base-plus.a-color-base.a-text-normal`}
	for _, sel := range titleSelectors {
		if el, err := card.QuerySelector(sel); err == nil && el != nil {
			if t, err := el.InnerText(); err == nil && strings.TrimSpace(t) != "" {
				p.Title = strings.TrimSpace(t)
				break
			}
		}
	}

	// 价格
	if el, err := card.QuerySelector(`.a-price .a-offscreen`); err == nil && el != nil {
		if t, err := el.InnerText(); err == nil {
			p.Price = strings.TrimSpace(t)
		}
	}

	// 评分（同时存原始字符串和数字）
	ratingSelectors := []string{`.a-icon-alt`, `[aria-label*="out of 5 stars"]`}
	for _, sel := range ratingSelectors {
		if el, err := card.QuerySelector(sel); err == nil && el != nil {
			if t, err := el.InnerText(); err == nil && strings.TrimSpace(t) != "" {
				p.Rating = strings.TrimSpace(t)
				p.ReviewScore = parseRatingScore(p.Rating)
				break
			}
		}
	}

	// 评论数
	reviewSelectors := []string{`span[aria-label*="ratings"]`, `.a-size-base.s-underline-text`}
	for _, sel := range reviewSelectors {
		if el, err := card.QuerySelector(sel); err == nil && el != nil {
			if t, err := el.GetAttribute("aria-label"); err == nil && t != "" {
				p.ReviewCount = strings.TrimSpace(t)
				break
			}
			if t, err := el.InnerText(); err == nil && t != "" {
				p.ReviewCount = strings.TrimSpace(t)
				break
			}
		}
	}

	// ASIN（从 data-asin 属性取）
	if asin, err := card.GetAttribute("data-asin"); err == nil && asin != "" {
		p.ASIN = asin
	}

	// 品牌
	if el, err := card.QuerySelector(`.a-size-base.a-color-secondary`); err == nil && el != nil {
		if t, err := el.InnerText(); err == nil && strings.TrimSpace(t) != "" {
			p.Brand = strings.TrimSpace(t)
		}
	}

	// Prime 标识
	if el, err := card.QuerySelector(`.s-prime`); err == nil && el != nil {
		p.IsPrime = true
	}

	// 商品图片
	if rules.FetchImage {
		if el, err := card.QuerySelector(`img.s-image`); err == nil && el != nil {
			if src, err := el.GetAttribute("src"); err == nil && src != "" {
				p.ImageURL = src
			}
		}
	}

	// URL
	if el, err := card.QuerySelector(`[data-cy="title-recipe"] a`); err == nil && el != nil {
		if href, err := el.GetAttribute("href"); err == nil && href != "" {
			if strings.HasPrefix(href, "http") {
				p.URL = href
			} else {
				p.URL = "https://www.amazon.com" + href
			}
		}
	}
	return p
}

// parseRatingScore 从 "4.5 out of 5 stars" 解析出 4.5
func parseRatingScore(s string) float64 {
	var score float64
	fmt.Sscanf(s, "%f", &score)
	return score
}

// parseReviewCount 从 "1,234 ratings" 解析出 1234
func parseReviewCount(s string) int {
	s = strings.ReplaceAll(s, ",", "")
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}

func fetchAmazonDescription(browser playwright.Browser, url string) string {
	page, err := newPage(browser)
	if err != nil {
		return ""
	}
	defer page.Close()

	proxy.RandomSleep(1, 3)
	if _, err = page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(20000),
	}); err != nil {
		return ""
	}

	// 尝试多个可能的描述选择器
	selectors := []string{
		`#productDescription`,
		`#feature-bullets`,
		`#aplus`,
	}
	for _, sel := range selectors {
		if el, err := page.QuerySelector(sel); err == nil && el != nil {
			if html, err := el.InnerHTML(); err == nil {
				return StripHTML(html)
			}
		}
	}
	return ""
}

// scrapeTikTokShop 抓取 TikTok Shop 搜索结果
func scrapeTikTokShop(pw *playwright.Playwright, keyword string, rules ScrapeRules) ([]Product, error) {
	browser, err := newBrowser(pw)
	if err != nil {
		return nil, err
	}
	defer browser.Close()

	page, err := newPage(browser)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://www.tiktok.com/search?q=%s&type=product", urlEncode(keyword))
	if _, err = page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
		Timeout:   playwright.Float(45000),
	}); err != nil {
		return nil, fmt.Errorf("TikTok Shop 导航失败: %w", err)
	}

	proxy.RandomSleep(4, 6)

	// TikTok 是 SPA，尝试多个可能的卡片选择器
	tiktokSelectors := []string{
		`div[data-e2e="search-card-container"]`,
		`div[class*="DivItemContainer"]`,
		`div[class*="search-card"]`,
		`[data-e2e*="card"]`,
		`div[class*="CardContainer"]`,
	}

	var cards []playwright.ElementHandle
	for _, sel := range tiktokSelectors {
		els, err := page.QuerySelectorAll(sel)
		if err == nil && len(els) > 0 {
			log.Printf("[TikTokShop] 使用选择器: %s, 找到 %d 个卡片", sel, len(els))
			cards = els
			break
		}
	}
	if len(cards) == 0 {
		return nil, fmt.Errorf("TikTok Shop 未找到商品卡片（页面可能需要登录或被反爬）")
	}

	var products []Product
	maxItems := rules.MaxItems
	if maxItems <= 0 {
		maxItems = 10
	}
	limit := maxItems
	if len(cards) < limit {
		limit = len(cards)
	}

	for i := 0; i < limit; i++ {
		p := extractTikTokCard(cards[i], rules)
		if p.Title == "" {
			continue
		}
		p.Source = "TikTokShop"
		products = append(products, p)
	}
	return products, nil
}

func extractTikTokCard(card playwright.ElementHandle, rules ScrapeRules) Product {
	var p Product

	if el, err := card.QuerySelector(`[data-e2e="search-card-desc"]`); err == nil && el != nil {
		if t, err := el.InnerText(); err == nil {
			p.Title = strings.TrimSpace(t)
		}
	}
	if el, err := card.QuerySelector(`[data-e2e="search-card-price"]`); err == nil && el != nil {
		if t, err := el.InnerText(); err == nil {
			p.Price = strings.TrimSpace(t)
		}
	}
	if el, err := card.QuerySelector(`[data-e2e="search-card-like-count"]`); err == nil && el != nil {
		if t, err := el.InnerText(); err == nil {
			p.ReviewCount = strings.TrimSpace(t) + " likes"
		}
	}
	if rules.FetchImage {
		if el, err := card.QuerySelector(`img`); err == nil && el != nil {
			if src, err := el.GetAttribute("src"); err == nil {
				p.ImageURL = src
			}
		}
	}
	return p
}

// urlEncode 简单 URL 编码关键词
func urlEncode(s string) string {
	return strings.ReplaceAll(s, " ", "+")
}
