// debug_page: 打开页面截图 + 打印前2000字节HTML，用于排查选择器问题
package main

import (
	"fmt"
	"log"
	"os"

	"ecom-analyzer/proxy"

	"github.com/playwright-community/playwright-go"
)

func main() {
	target := "amazon" // 默认
	if len(os.Args) > 1 {
		target = os.Args[1]
	}

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
		log.Fatalf("context 创建失败: %v", err)
	}

	page, err := ctx.NewPage()
	if err != nil {
		log.Fatalf("page 创建失败: %v", err)
	}

	var url string
	switch target {
	case "tiktok":
		url = "https://www.tiktok.com/search?q=Smart+Pet+Feeder&type=product"
	default:
		url = "https://www.amazon.com/s?k=Smart+Pet+Feeder&sort=review-rank"
	}

	log.Printf("正在访问: %s", url)
	if _, err = page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		log.Fatalf("导航失败: %v", err)
	}

	proxy.RandomSleep(3, 5)

	// 截图
	screenshotFile := fmt.Sprintf("debug_%s.png", target)
	if _, err = page.Screenshot(playwright.PageScreenshotOptions{
		Path:     playwright.String(screenshotFile),
		FullPage: playwright.Bool(false),
	}); err != nil {
		log.Printf("截图失败: %v", err)
	} else {
		log.Printf("📸 截图已保存: %s", screenshotFile)
	}

	// 打印页面 title
	title, _ := page.Title()
	log.Printf("页面标题: %s", title)

	// 打印当前 URL（检测是否被重定向）
	log.Printf("当前 URL: %s", page.URL())

	// 打印 HTML 片段（前 3000 字节）
	html, err := page.Content()
	if err != nil {
		log.Printf("获取 HTML 失败: %v", err)
	} else {
		if len(html) > 3000 {
			html = html[:3000]
		}
		fmt.Println("\n===== HTML 片段 =====")
		fmt.Println(html)
	}

	// 尝试 Amazon 选择器
	if target == "amazon" {
		cards, err := page.QuerySelectorAll(`div[data-component-type="s-search-result"]`)
		if err != nil {
			log.Printf("QuerySelectorAll 错误: %v", err)
		} else {
			log.Printf("找到商品卡片数量: %d", len(cards))
		}
	}

	// 尝试 TikTok 选择器
	if target == "tiktok" {
		selectors := []string{
			`div[data-e2e="search-card-container"]`,
			`div[class*="DivItemContainer"]`,
			`div[class*="search-card"]`,
			`[data-e2e*="card"]`,
		}
		for _, sel := range selectors {
			els, err := page.QuerySelectorAll(sel)
			if err == nil {
				log.Printf("选择器 %q 找到 %d 个元素", sel, len(els))
			}
		}
	}
}
