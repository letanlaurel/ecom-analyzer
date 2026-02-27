// cmd/debug_tiktok 抓取 TikTok 标签页 HTML 并保存，用于分析真实 DOM 结构
// 用法: go run ./cmd/debug_tiktok -tag PetHealth
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"ecom-analyzer/proxy"

	"github.com/playwright-community/playwright-go"
)

func main() {
	tag := flag.String("tag", "PetHealth", "标签名（不含#）")
	flag.Parse()

	pw, err := playwright.Run()
	if err != nil {
		log.Fatal(err)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(false), // 有头模式，方便观察
		Args: []string{
			"--no-sandbox",
			"--disable-blink-features=AutomationControlled",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer browser.Close()

	ctx, _ := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent:  playwright.String(proxy.GetRandomUserAgent()),
		Viewport:   &playwright.Size{Width: 1440, Height: 900},
		Locale:     playwright.String("en-US"),
		TimezoneId: playwright.String("America/New_York"),
	})
	page, _ := ctx.NewPage()
	_ = page.AddInitScript(playwright.Script{Content: playwright.String(
		`Object.defineProperty(navigator, 'webdriver', { get: () => undefined });`,
	)})

	url := fmt.Sprintf("https://www.tiktok.com/tag/%s", *tag)
	log.Printf("访问: %s", url)
	if _, err = page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
		Timeout:   playwright.Float(45000),
	}); err != nil {
		log.Fatalf("导航失败: %v", err)
	}

	proxy.RandomSleep(3, 5)

	// 滚动一次触发懒加载
	page.Evaluate(`window.scrollBy(0, window.innerHeight * 2)`)
	time.Sleep(3 * time.Second)

	// 尝试找卡片并打印第一个卡片的 outerHTML
	selectors := []string{
		`div[data-e2e="challenge-item"]`,
		`div[class*="DivItemContainer"]`,
		`div[class*="video-feed-item"]`,
		`[data-e2e*="video-item"]`,
		`div[class*="VideoFeed"] > div`,
		`div[class*="CommonItemList"] > div`,
	}

	for _, sel := range selectors {
		cards, err := page.QuerySelectorAll(sel)
		if err == nil && len(cards) > 0 {
			log.Printf("✅ 命中选择器: %s  找到 %d 个卡片", sel, len(cards))
			if html, err := cards[0].InnerHTML(); err == nil {
				fname := fmt.Sprintf("debug_tiktok_card_%s.html", *tag)
				os.WriteFile(fname, []byte(html), 0644)
				log.Printf("📄 第一个卡片 HTML 已保存: %s", fname)
			}
			break
		} else {
			log.Printf("❌ 未命中: %s", sel)
		}
	}

	// 同时保存完整页面 HTML
	fullHTML, _ := page.Content()
	fname := fmt.Sprintf("debug_tiktok_full_%s.html", *tag)
	os.WriteFile(fname, []byte(fullHTML), 0644)
	log.Printf("📄 完整页面 HTML 已保存: %s", fname)

	// 打印页面中所有 data-e2e 属性值，帮助定位选择器
	result, err := page.Evaluate(`
		() => {
			const els = document.querySelectorAll('[data-e2e]');
			const vals = [...new Set([...els].map(e => e.getAttribute('data-e2e')))];
			return vals;
		}
	`)
	if err == nil {
		if vals, ok := result.([]interface{}); ok {
			log.Printf("📋 页面中所有 data-e2e 值 (%d 个):", len(vals))
			for _, v := range vals {
				log.Printf("   · %v", v)
			}
		}
	}

	// 打印包含数字的 strong/span 元素，帮助定位计数选择器
	result2, err := page.Evaluate(`
		() => {
			const els = [...document.querySelectorAll('strong, [class*="count" i], [class*="Count"]')];
			return els.slice(0, 30).map(e => ({
				tag: e.tagName,
				cls: e.className,
				e2e: e.getAttribute('data-e2e') || '',
				text: e.innerText.trim().slice(0, 20),
			}));
		}
	`)
	if err == nil {
		if items, ok := result2.([]interface{}); ok {
			log.Printf("\n📋 strong/count 元素 (前30个):")
			for _, item := range items {
				if m, ok := item.(map[string]interface{}); ok {
					cls := fmt.Sprintf("%v", m["cls"])
					if len(cls) > 60 {
						cls = cls[:60] + "..."
					}
					log.Printf("   [%v] e2e=%v | class=%v | text=%v",
						m["tag"], m["e2e"], cls, m["text"])
				}
			}
		}
	}

	// 打印视频链接附近的 DOM 结构
	result3, err := page.Evaluate(`
		() => {
			const link = document.querySelector('a[href*="/video/"]');
			if (!link) return 'no video link found';
			const card = link.closest('div[class]') || link.parentElement;
			return card ? card.outerHTML.slice(0, 3000) : 'no card found';
		}
	`)
	if err == nil {
		snippet := fmt.Sprintf("%v", result3)
		// 把 class 名中的空格替换成换行，方便阅读
		snippet = strings.ReplaceAll(snippet, "><", ">\n<")
		fname2 := fmt.Sprintf("debug_tiktok_snippet_%s.html", *tag)
		os.WriteFile(fname2, []byte(snippet), 0644)
		log.Printf("📄 视频卡片片段已保存: %s", fname2)
	}

	log.Println("✅ 调试完成，请查看生成的 HTML 文件")
}
