package tiktok

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"ecom-analyzer/proxy"

	"github.com/playwright-community/playwright-go"
)

// scrapeHashtag 抓取指定标签下的视频
// 策略1: 拦截 TikTok 内部 API XHR 响应（有完整互动数据）
// 策略2: DOM 抓取视频卡片（互动数据在标签页缩略图中不渲染，但能拿到URL/作者/日期）
func scrapeHashtag(pw *playwright.Playwright, tag string) ([]Video, error) {
	browser, err := launchStealthBrowser(pw)
	if err != nil {
		return nil, fmt.Errorf("[%s] 浏览器启动失败: %w", tag, err)
	}
	defer browser.Close()

	page, err := newStealthPage(browser)
	if err != nil {
		return nil, fmt.Errorf("[%s] 页面创建失败: %w", tag, err)
	}
	defer page.Close()

	cleanTag := strings.TrimPrefix(tag, "#")
	targetURL := fmt.Sprintf("https://www.tiktok.com/tag/%s", cleanTag)
	log.Printf("[%s] 正在访问: %s", tag, targetURL)

	// 策略1: 用 page.Route 拦截 item_list 响应（可靠读取响应体）
	var apiVideos []Video
	var apiMu sync.Mutex
	page.Route("**/api/challenge/item_list/**", func(route playwright.Route) {
		resp, err := route.Fetch()
		if err != nil {
			log.Printf("[%s] Route fetch失败: %v", tag, err)
			route.Continue()
			return
		}
		body, err := resp.Body()
		if err != nil || len(body) < 100 {
			route.Fulfill(playwright.RouteFulfillOptions{Response: resp})
			return
		}
		preview := string(body)
		if len(preview) > 300 {
			preview = preview[:300]
		}
		log.Printf("[%s] item_list响应体: %s", tag, preview)
		videos := parseAPIResponse(body, tag)
		if len(videos) > 0 {
			log.Printf("[%s] API拦截成功: %d 条", tag, len(videos))
			apiMu.Lock()
			apiVideos = append(apiVideos, videos...)
			apiMu.Unlock()
		}
		route.Fulfill(playwright.RouteFulfillOptions{Response: resp})
	})
	// 同时保留 response 事件打印其他 API URL
	page.On("response", func(resp playwright.Response) {
		u := resp.URL()
		if strings.Contains(u, "tiktok.com/api") || strings.Contains(u, "tiktok.com/v1") {
			log.Printf("[%s] API: %s", tag, u[:min(len(u), 80)])
		}
	})

	if _, err = page.Goto(targetURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		log.Printf("[%s] 页面加载超时（继续处理）", tag)
	}
	// 等待首批 item_list 请求触发
	time.Sleep(4 * time.Second)

	// 滚动触发更多 API 请求（每次滚动都会触发新的 item_list 分页）
	for i := 0; i < 5; i++ {
		page.Evaluate(`window.scrollBy(0, window.innerHeight * 3)`)
		time.Sleep(2 * time.Second)
	}
	time.Sleep(2 * time.Second)

	apiMu.Lock()
	captured := make([]Video, len(apiVideos))
	copy(captured, apiVideos)
	apiMu.Unlock()

	if len(captured) > 0 {
		log.Printf("[%s] 策略1成功: %d 条视频（含互动数据）", tag, len(captured))
		return filterAndScore(captured), nil
	}

	log.Printf("[%s] 策略1未拦截到数据，改用策略2: DOM抓取", tag)

	// 策略2: DOM 抓取视频卡片
	videos, err := extractFromDOM(page, tag)
	if err != nil || len(videos) == 0 {
		return nil, fmt.Errorf("[%s] 所有策略均失败: %v", tag, err)
	}
	log.Printf("[%s] 策略2 DOM抓取: %d 条视频", tag, len(videos))
	return filterAndScore(videos), nil
}

// extractFromDOM 从标签页 DOM 提取视频卡片数据
func extractFromDOM(page playwright.Page, tag string) ([]Video, error) {
	result, err := page.Evaluate(`() => {
		const seen = new Set();
		const out = [];
		for (const a of document.querySelectorAll('a[href*="/video/"]')) {
			const href = a.href || '';
			const m = href.match(/\/@([^/?#]+)\/video\/(\d+)/);
			if (!m || seen.has(m[2])) continue;
			seen.add(m[2]);

			let publishedAt = '';
			let likeCount = 0, commentCount = 0, shareCount = 0, playCount = 0;

			// 向上找最近的卡片容器，查找互动数据和日期
			let card = a;
			for (let i = 0; i < 10; i++) {
				card = card.parentElement;
				if (!card) break;

				if (!publishedAt) {
					const text = card.innerText || '';
					const dm = text.match(/(\d{4}-\d{2}-\d{2})/);
					if (dm) publishedAt = dm[1];
				}

				const likeEl = card.querySelector('[data-e2e="like-count"]');
				if (likeEl) likeCount = parseInt(likeEl.innerText.replace(/[^0-9]/g,'')) || 0;
				const commentEl = card.querySelector('[data-e2e="comment-count"]');
				if (commentEl) commentCount = parseInt(commentEl.innerText.replace(/[^0-9]/g,'')) || 0;
				const shareEl = card.querySelector('[data-e2e="share-count"]');
				if (shareEl) shareCount = parseInt(shareEl.innerText.replace(/[^0-9]/g,'')) || 0;

				if (publishedAt || likeCount > 0) break;
			}

			out.push({ url: href, author: m[1], videoId: m[2], publishedAt, likeCount, commentCount, shareCount, playCount });
			if (out.length >= 50) break;
		}
		return JSON.stringify(out);
	}`)
	if err != nil {
		return nil, fmt.Errorf("DOM评估失败: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("DOM返回nil")
	}
	raw, ok := result.(string)
	if !ok || raw == "null" || raw == "" || raw == "[]" {
		return nil, fmt.Errorf("DOM无视频数据")
	}

	var items []struct {
		URL          string `json:"url"`
		Author       string `json:"author"`
		VideoID      string `json:"videoId"`
		PublishedAt  string `json:"publishedAt"`
		LikeCount    int64  `json:"likeCount"`
		CommentCount int64  `json:"commentCount"`
		ShareCount   int64  `json:"shareCount"`
		PlayCount    int64  `json:"playCount"`
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("无视频条目")
	}

	var videos []Video
	for _, item := range items {
		v := Video{
			URL:          item.URL,
			Author:       item.Author,
			Hashtag:      tag,
			LikeCount:    item.LikeCount,
			CommentCount: item.CommentCount,
			ShareCount:   item.ShareCount,
			PlayCount:    item.PlayCount,
		}
		if item.PublishedAt != "" {
			if t, err := time.Parse("2006-01-02", item.PublishedAt); err == nil {
				v.PublishedAt = t
			}
		}
		videos = append(videos, v)
	}
	return videos, nil
}

// parseAPIResponse 解析 TikTok 内部 API 响应
func parseAPIResponse(body []byte, tag string) []Video {
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	var itemListRaw json.RawMessage
	for _, key := range []string{"itemList", "item_list", "items"} {
		if v, ok := resp[key]; ok {
			itemListRaw = v
			break
		}
	}
	if itemListRaw == nil {
		if dataRaw, ok := resp["data"]; ok {
			var data map[string]json.RawMessage
			if json.Unmarshal(dataRaw, &data) == nil {
				for _, key := range []string{"itemList", "item_list", "items"} {
					if v, ok := data[key]; ok {
						itemListRaw = v
						break
					}
				}
			}
		}
	}
	if itemListRaw == nil {
		return nil
	}
	var items []struct {
		ID     string `json:"id"`
		Author struct {
			UniqueID string `json:"uniqueId"`
		} `json:"author"`
		Stats struct {
			PlayCount    int64 `json:"playCount"`
			DiggCount    int64 `json:"diggCount"`
			CommentCount int64 `json:"commentCount"`
			ShareCount   int64 `json:"shareCount"`
		} `json:"stats"`
		StatsV2 struct {
			PlayCount    string `json:"playCount"`
			DiggCount    string `json:"diggCount"`
			CommentCount string `json:"commentCount"`
			ShareCount   string `json:"shareCount"`
		} `json:"statsV2"`
		CreateTime int64 `json:"createTime"`
	}
	if err := json.Unmarshal(itemListRaw, &items); err != nil {
		return nil
	}
	var videos []Video
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		v := Video{
			URL:     fmt.Sprintf("https://www.tiktok.com/@%s/video/%s", item.Author.UniqueID, item.ID),
			Author:  item.Author.UniqueID,
			Hashtag: tag,
		}
		if item.CreateTime > 0 {
			v.PublishedAt = time.Unix(item.CreateTime, 0)
		}
		v.PlayCount = item.Stats.PlayCount
		v.LikeCount = item.Stats.DiggCount
		v.CommentCount = item.Stats.CommentCount
		v.ShareCount = item.Stats.ShareCount
		if v.PlayCount == 0 {
			v.PlayCount = parseCount(item.StatsV2.PlayCount)
			v.LikeCount = parseCount(item.StatsV2.DiggCount)
			v.CommentCount = parseCount(item.StatsV2.CommentCount)
			v.ShareCount = parseCount(item.StatsV2.ShareCount)
		}
		videos = append(videos, v)
	}
	return videos
}

// filterAndScore 过滤超过30天的视频并计算 GrowthScore
func filterAndScore(videos []Video) []Video {
	cutoff := time.Now().AddDate(0, 0, -30)
	var result []Video
	for _, v := range videos {
		// 零值（未知日期）保留；有日期且超过30天才过滤
		if !v.PublishedAt.IsZero() && v.PublishedAt.Before(cutoff) {
			continue
		}
		v.GrowthScore = CalcGrowthScore(v)
		result = append(result, v)
	}
	return result
}

// parseCount 解析 "1.2M" / "34.5K" / "1,234" 等格式为 int64
func parseCount(s string) int64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0
	}
	multiplier := int64(1)
	if strings.HasSuffix(s, "M") {
		multiplier = 1_000_000
		s = strings.TrimSuffix(s, "M")
	} else if strings.HasSuffix(s, "K") {
		multiplier = 1_000
		s = strings.TrimSuffix(s, "K")
	}
	s = strings.ReplaceAll(s, ",", "")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(f * float64(multiplier))
}

// launchStealthBrowser 启动带反检测配置的浏览器
func launchStealthBrowser(pw *playwright.Playwright) (playwright.Browser, error) {
	opts := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Args: []string{
			"--no-sandbox",
			"--disable-blink-features=AutomationControlled",
			"--disable-dev-shm-usage",
		},
	}
	if p := proxy.GetRandomProxy(); p != "" {
		opts.Proxy = &playwright.Proxy{Server: p}
	}
	return pw.Chromium.Launch(opts)
}

// newStealthPage 创建注入反检测脚本的页面
func newStealthPage(browser playwright.Browser) (playwright.Page, error) {
	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent:  playwright.String(proxy.GetRandomUserAgent()),
		Viewport:   &playwright.Size{Width: 1366, Height: 768},
		Locale:     playwright.String("en-US"),
		TimezoneId: playwright.String("America/New_York"),
		ExtraHttpHeaders: map[string]string{
			"Accept-Language": "en-US,en;q=0.9",
		},
	})
	if err != nil {
		return nil, err
	}
	page, err := ctx.NewPage()
	if err != nil {
		return nil, err
	}
	_ = page.AddInitScript(playwright.Script{Content: playwright.String(`
		Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
		Object.defineProperty(navigator, 'languages', { get: () => ['en-US', 'en'] });
		window.chrome = { runtime: {} };
	`)})
	return page, nil
}
