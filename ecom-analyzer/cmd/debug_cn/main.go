package main

import (
	"fmt"
	"os"

	"github.com/playwright-community/playwright-go"
)

func main() {
	platform := "jd"
	if len(os.Args) > 1 {
		platform = os.Args[1]
	}
	keyword := "智能宠物喂食器"
	if len(os.Args) > 2 {
		keyword = os.Args[2]
	}

	pw, err := playwright.Run()
	if err != nil {
		panic(err)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(false),
	})
	if err != nil {
		panic(err)
	}
	defer browser.Close()

	ctx, _ := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: playwright.String("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"),
		ExtraHttpHeaders: map[string]string{
			"Accept-Language": "zh-CN,zh;q=0.9",
		},
		Locale: playwright.String("zh-CN"),
	})
	page, _ := ctx.NewPage()

	homepages := map[string]string{
		"taobao":    "https://www.taobao.com",
		"jd":        "https://www.jd.com",
		"pinduoduo": "https://www.pinduoduo.com",
	}
	searchURLs := map[string]string{
		"taobao":    fmt.Sprintf("https://s.taobao.com/search?q=%s", urlEncode(keyword)),
		"jd":        fmt.Sprintf("https://search.jd.com/Search?keyword=%s&enc=utf-8", urlEncode(keyword)),
		"pinduoduo": fmt.Sprintf("https://mobile.yangkeduo.com/search_result.html?search_key=%s", urlEncode(keyword)),
	}

	hp, ok := homepages[platform]
	if !ok {
		fmt.Printf("未知平台: %s，可选: taobao, jd, pinduoduo\n", platform)
		os.Exit(1)
	}

	fmt.Printf("=== 调试平台: %s | 关键词: %s ===\n", platform, keyword)
	fmt.Printf("预热首页: %s\n", hp)
	page.Goto(hp, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	})
	fmt.Println("等待 3 秒...")
	page.WaitForTimeout(3000)

	searchURL := searchURLs[platform]
	fmt.Printf("访问搜索页: %s\n", searchURL)
	_, err = page.Goto(searchURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(45000),
	})
	if err != nil {
		fmt.Printf("导航失败: %v\n", err)
	}
	fmt.Println("等待 5 秒渲染...")
	page.WaitForTimeout(5000)

	// 滚动
	page.Evaluate(`() => window.scrollTo(0, document.body.scrollHeight / 2)`)
	page.WaitForTimeout(1500)
	page.Evaluate(`() => window.scrollTo(0, document.body.scrollHeight)`)
	page.WaitForTimeout(1500)

	// 截图
	screenshotPath := fmt.Sprintf("debug_%s.png", platform)
	page.Screenshot(playwright.PageScreenshotOptions{
		Path:     playwright.String(screenshotPath),
		FullPage: playwright.Bool(true),
	})
	fmt.Printf("截图: %s\n", screenshotPath)

	// 保存 HTML
	html, _ := page.Content()
	htmlPath := fmt.Sprintf("debug_%s.html", platform)
	os.WriteFile(htmlPath, []byte(html), 0644)
	fmt.Printf("HTML: %s (%d bytes)\n", htmlPath, len(html))

	// 当前 URL（检查是否被重定向）
	currentURL := page.URL()
	fmt.Printf("当前 URL: %s\n", currentURL)

	// 页面标题
	title, _ := page.Title()
	fmt.Printf("页面标题: %s\n", title)

	fmt.Println("\n=== 选择器诊断 ===")
	switch platform {
	case "taobao":
		debugSelectors(page, []string{
			`[data-item-id]`,
			`[class*="doubleCardWrapper"]`,
			`[class*="CardContainer"]`,
			`a[href*="item.taobao.com"]`,
			`a[href*="detail.tmall.com"]`,
			`[class*="title"]`,
		})
		// 输出第一个商品卡片内容
		if el, _ := page.QuerySelector(`[data-item-id]`); el != nil {
			h, _ := el.InnerHTML()
			fmt.Printf("\n第一个 [data-item-id] 内容 (前 2000 字符):\n%s\n", truncate(h, 2000))
		}
		// 输出链接数量
		links, _ := page.QuerySelectorAll(`a[href*="item.taobao.com"]`)
		fmt.Printf("\nitem.taobao.com 链接数: %d\n", len(links))
		links2, _ := page.QuerySelectorAll(`a[href*="detail.tmall.com"]`)
		fmt.Printf("detail.tmall.com 链接数: %d\n", len(links2))

	case "jd":
		debugSelectors(page, []string{
			`#J_goodsList li.gl-item`,
			`li[data-sku]`,
			`li[data-pid]`,
			`.p-name`,
			`.p-price`,
			`a[href*="item.jd.com"]`,
		})
		if el, _ := page.QuerySelector(`#J_goodsList li.gl-item`); el != nil {
			h, _ := el.InnerHTML()
			fmt.Printf("\n第一个 li.gl-item 内容 (前 2000 字符):\n%s\n", truncate(h, 2000))
		} else if el, _ := page.QuerySelector(`li[data-sku]`); el != nil {
			h, _ := el.InnerHTML()
			fmt.Printf("\n第一个 li[data-sku] 内容 (前 2000 字符):\n%s\n", truncate(h, 2000))
		}

	case "pinduoduo":
		debugSelectors(page, []string{
			`[class*="goods-item"]`,
			`[class*="GoodsItem"]`,
			`[class*="search-item"]`,
			`[class*="SearchItem"]`,
			`[data-goods-id]`,
			`a[href*="goods_id"]`,
			`a[href*="goods.html"]`,
		})
		// 输出所有 class 包含 item 的元素数量
		result, _ := page.Evaluate(`() => {
			const all = document.querySelectorAll('*');
			const classes = new Set();
			for (const el of all) {
				for (const c of el.classList) {
					if (c.toLowerCase().includes('item') || c.toLowerCase().includes('goods') || c.toLowerCase().includes('product')) {
						classes.add(c);
					}
				}
			}
			return [...classes].slice(0, 30);
		}`)
		fmt.Printf("\n包含 item/goods/product 的 class 名 (前30个): %v\n", result)

		links, _ := page.QuerySelectorAll(`a[href*="goods_id"]`)
		fmt.Printf("goods_id 链接数: %d\n", len(links))
		if len(links) > 0 {
			href, _ := links[0].GetAttribute("href")
			fmt.Printf("第一个链接: %s\n", href)
			// 输出父容器
			h, _ := links[0].Evaluate(`el => { let p = el; for(let i=0;i<5;i++){if(!p.parentElement)break;p=p.parentElement;if(p.offsetHeight>100)break;} return p.outerHTML.substring(0,1000); }`)
			fmt.Printf("父容器: %v\n", h)
		}
	}

	fmt.Println("\n按 Enter 关闭浏览器...")
	fmt.Scanln()
}

func debugSelectors(page playwright.Page, selectors []string) {
	for _, sel := range selectors {
		els, err := page.QuerySelectorAll(sel)
		if err != nil {
			fmt.Printf("  %-55s ERROR\n", sel)
		} else {
			fmt.Printf("  %-55s 找到 %d 个\n", sel, len(els))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(截断)"
}

func urlEncode(s string) string {
	result := ""
	for _, c := range s {
		if c == ' ' {
			result += "+"
		} else {
			result += string(c)
		}
	}
	return result
}
