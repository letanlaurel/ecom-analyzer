// cmd/tiktok_monitor 是 TikTok 趋势监控的独立命令行入口
//
// 用法:
//
//	go run ./cmd/tiktok_monitor -tags "#PetHealth,#DogAnxiety" -workers 2 -min-score 0.05
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"ecom-analyzer/tiktok"
)

func main() {
	tagsFlag := flag.String("tags", "#PetHealth,#DogAnxiety", "逗号分隔的监控标签，如 #PetHealth,#DogAnxiety")
	workers := flag.Int("workers", 2, "并发爬虫数量")
	minScore := flag.Float64("min-score", 0.0, "GrowthScore 最低阈值，0 表示不过滤")
	flag.Parse()

	hashtags := splitTags(*tagsFlag)
	if len(hashtags) == 0 {
		log.Fatal("请至少指定一个标签")
	}

	log.Printf("🚀 开始监控标签: %v", hashtags)
	log.Printf("   并发数: %d | 最低分: %.4f", *workers, *minScore)

	monitor := tiktok.NewMonitor(
		hashtags,
		tiktok.WithWorkerCount(*workers),
		tiktok.WithMinScore(*minScore),
	)

	videos, err := monitor.Run()
	if err != nil {
		log.Fatalf("监控失败: %v", err)
	}

	if len(videos) == 0 {
		log.Println("⚠️  未抓取到符合条件的视频")
		return
	}

	// 输出 JSON 报告
	out, _ := json.MarshalIndent(videos, "", "  ")
	fmt.Println("\n========== TikTok 趋势监控报告 ==========")
	fmt.Println(string(out))

	// 保存到文件
	filename := fmt.Sprintf("tiktok_trends_%s.json", time.Now().Format("20060102_150405"))
	if err = os.WriteFile(filename, out, 0644); err != nil {
		log.Printf("写入文件失败: %v", err)
	} else {
		log.Printf("📄 报告已保存至: %s", filename)
	}
}

func splitTags(s string) []string {
	parts := strings.Split(s, ",")
	var tags []string
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}
