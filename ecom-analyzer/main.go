package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"ecom-analyzer/ai"
	"ecom-analyzer/scraper"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法: ecom-analyzer <关键词>")
		fmt.Println("示例: ecom-analyzer \"Smart Pet Feeder\"")
		os.Exit(1)
	}

	keyword := strings.Join(os.Args[1:], " ")
	log.Printf("🔍 开始分析关键词: %s", keyword)

	// Step 1: 并发抓取
	log.Println("📦 正在抓取商品数据...")
	products, err := scraper.ScrapeAll(keyword)
	if err != nil {
		log.Fatalf("抓取失败: %v", err)
	}
	if len(products) == 0 {
		log.Fatal("未抓取到任何商品数据，请检查网络或代理配置")
	}
	log.Printf("✅ 共抓取到 %d 个商品", len(products))

	// Step 2: AI 分析
	log.Println("🤖 正在调用 AI 分析...")
	report, err := ai.Analyze(keyword, products)
	if err != nil {
		log.Fatalf("AI 分析失败: %v", err)
	}

	// Step 3: 输出 JSON 报告
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		log.Fatalf("JSON 序列化失败: %v", err)
	}

	fmt.Println("\n========== 选品分析报告 ==========")
	fmt.Println(string(out))

	// 同时写入文件
	filename := fmt.Sprintf("report_%s.json", strings.ReplaceAll(keyword, " ", "_"))
	if err = os.WriteFile(filename, out, 0644); err != nil {
		log.Printf("写入文件失败: %v", err)
	} else {
		log.Printf("📄 报告已保存至: %s", filename)
	}
}
