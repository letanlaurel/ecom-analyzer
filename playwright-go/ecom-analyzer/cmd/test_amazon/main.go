// test_amazon: 单独测试 Amazon 抓取（不抓详情页，只抓列表）
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"ecom-analyzer/proxy"

	"github.com/playwright-community/playwright-go"
)

func main() {
	keyword := "Smart Pet Feeder"

	pw, err := playwright.Run()
	if err != nil {
		log.Fatalf("playwright 启动失败: %v", err)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		log.Fatalf("浏览器启动失败: %v", err)
	}
	defer browser.Close()

	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: playwright.String(proxy.GetRandomUserAgent()),
		ExtraHttpHeaders: map[string]string{
			"Accept-Language": "en-US,en;q=0.9",
		},
	})
	if err != nil {
		log.Fatalf("context 失败: %v", err)
	}

	page, err := ctx.NewPage()
	if err != nil {
		log.Fatalf("page 失败: %v", err)
	}

	url := fmt.Sprintf("https://www.amazon.com/s?k=%s&sort=review-rank", strings.ReplaceAll(keyword, " ", "+"))
	log.Printf("访问: %s", url)

	if _, err = page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		log.Fatalf("导航失败: %v", err)
	}

	proxy.RandomSleep(2, 3)

	cards, err := page.QuerySelectorAll(`div[data-component-type="s-search-result"]`)
	if err != nil || len(cards) == 0 {
		log.Fatalf("未找到商品卡片, err=%v", err)
	}
	log.Printf("找到 %d 个卡片", len(cards))

	// 打印第一个卡片的 HTML，用于确认选择器
	if html, err := cards[0].InnerHTML(); err == nil {
		preview := html
		if len(preview) > 4000 {
			preview = preview[:4000]
		}
		fmt.Println("===== 第一个卡片 HTML =====")
		fmt.Println(preview)
		fmt.Println("===========================")
	}

	// 探测各种可能的标题选择器
	titleSelectors := []string{
		`h2 a span`,
		`h2 span`,
		`[data-cy="title-recipe"] h2 a span`,
		`[data-cy="title-recipe"] span`,
		`.a-size-medium.a-color-base.a-text-normal`,
		`.a-size-base-plus.a-color-base.a-text-normal`,
		`h2.a-size-mini a span`,
		`span.a-text-normal`,
	}
	log.Println("探测标题选择器:")
	for _, sel := range titleSelectors {
		if el, err := cards[0].QuerySelector(sel); err == nil && el != nil {
			if t, err := el.InnerText(); err == nil && t != "" {
				log.Printf("  ✅ %q => %q", sel, t[:min(len(t), 60)])
			}
		} else {
			log.Printf("  ❌ %q", sel)
		}
	}

	type Product struct {
		Title       string `json:"title"`
		Price       string `json:"price"`
		Rating      string `json:"rating"`
		ReviewCount string `json:"review_count"`
		URL         string `json:"url"`
	}

	var products []Product
	limit := 10
	if len(cards) < limit {
		limit = len(cards)
	}

	for i := 0; i < limit; i++ {
		card := cards[i]
		var p Product

		if el, err := card.QuerySelector(`h2 a span`); err == nil && el != nil {
			if t, _ := el.InnerText(); t != "" {
				p.Title = strings.TrimSpace(t)
			}
		}
		if el, err := card.QuerySelector(`.a-price .a-offscreen`); err == nil && el != nil {
			if t, _ := el.InnerText(); t != "" {
				p.Price = strings.TrimSpace(t)
			}
		}
		if el, err := card.QuerySelector(`.a-icon-alt`); err == nil && el != nil {
			if t, _ := el.InnerText(); t != "" {
				p.Rating = strings.TrimSpace(t)
			}
		}
		if el, err := card.QuerySelector(`span[aria-label*="ratings"]`); err == nil && el != nil {
			if t, _ := el.GetAttribute("aria-label"); t != "" {
				p.ReviewCount = strings.TrimSpace(t)
			}
		}
		if el, err := card.QuerySelector(`h2 a`); err == nil && el != nil {
			if href, _ := el.GetAttribute("href"); href != "" {
				p.URL = "https://www.amazon.com" + href
			}
		}

		if p.Title != "" {
			products = append(products, p)
		}
	}

	log.Printf("成功提取 %d 个商品", len(products))
	out, _ := json.MarshalIndent(products, "", "  ")
	fmt.Println(string(out))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
