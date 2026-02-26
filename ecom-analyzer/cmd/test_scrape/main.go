package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"ecom-analyzer/scraper"
)

func main() {
	keyword := "Smart Pet Feeder"
	if len(os.Args) > 1 {
		keyword = strings.Join(os.Args[1:], " ")
	}

	log.Printf("🔍 测试抓取关键词: %s", keyword)

	products, err := scraper.ScrapeAll(keyword)
	if err != nil {
		log.Fatalf("❌ 抓取失败: %v", err)
	}

	if len(products) == 0 {
		log.Fatal("❌ 未抓取到任何商品，请检查网络/选择器")
	}

	log.Printf("✅ 共抓取到 %d 个商品\n", len(products))

	out, _ := json.MarshalIndent(products, "", "  ")
	fmt.Println(string(out))
}
