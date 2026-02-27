// Package tiktok 提供 TikTok 热门趋势监控功能。
//
// 架构：生产者-消费者模式
//   - Monitor.Run() 将标签列表写入 taskCh（生产者）
//   - workerCount 个 Goroutine 并发消费任务，结果写入 resultCh（消费者）
//   - 结果收集协程汇总后返回
//
// 扩展说明：
//   若未来需要监控竞品 Shopify 店铺，只需实现相同的 Scraper 接口，
//   并在 Monitor 中注册新的 scraper 即可，无需改动调度逻辑。
package tiktok

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
)

// Scraper 抓取器接口，便于未来扩展（如 Shopify 竞品监控）
type Scraper interface {
	// Scrape 抓取指定标签/关键词下的视频/商品数据
	Scrape(pw *playwright.Playwright, target string) ([]Video, error)
}

// tiktokScraper 实现 Scraper 接口，对应 TikTok 标签页抓取
type tiktokScraper struct{}

func (t *tiktokScraper) Scrape(pw *playwright.Playwright, target string) ([]Video, error) {
	return scrapeHashtag(pw, target)
}

// Monitor TikTok 趋势监控器
type Monitor struct {
	hashtags    []string // 监控的标签列表，如 ["#PetHealth", "#DogAnxiety"]
	workerCount int      // 并发爬虫数量
	minScore    float64  // GrowthScore 最低阈值，低于此值过滤
	scraper     Scraper  // 可替换的抓取器
}

// MonitorOption 函数式选项，方便扩展配置
type MonitorOption func(*Monitor)

// WithWorkerCount 设置并发爬虫数量（默认 3）
func WithWorkerCount(n int) MonitorOption {
	return func(m *Monitor) { m.workerCount = n }
}

// WithMinScore 设置 GrowthScore 最低阈值（默认 0，不过滤）
func WithMinScore(score float64) MonitorOption {
	return func(m *Monitor) { m.minScore = score }
}

// WithScraper 替换抓取器实现（用于测试或扩展其他平台）
func WithScraper(s Scraper) MonitorOption {
	return func(m *Monitor) { m.scraper = s }
}

// NewMonitor 创建监控器
func NewMonitor(hashtags []string, opts ...MonitorOption) *Monitor {
	m := &Monitor{
		hashtags:    hashtags,
		workerCount: 3,
		scraper:     &tiktokScraper{},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Run 启动监控，返回按 GrowthScore 降序排列的视频列表
func (m *Monitor) Run() ([]Video, error) {
	if len(m.hashtags) == 0 {
		return nil, fmt.Errorf("hashtags 列表为空")
	}

	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("playwright 启动失败: %w", err)
	}
	defer pw.Stop()

	taskCh := make(chan Task, len(m.hashtags))
	resultCh := make(chan Result, len(m.hashtags))

	// 生产者：将所有标签任务写入 channel
	go func() {
		for _, tag := range m.hashtags {
			taskCh <- Task{Hashtag: tag}
		}
		close(taskCh)
	}()

	// 消费者：启动 workerCount 个并发爬虫
	var wg sync.WaitGroup
	for i := 0; i < m.workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range taskCh {
				log.Printf("[worker-%d] 开始抓取标签: %s", workerID, task.Hashtag)
				videos, err := m.scraper.Scrape(pw, task.Hashtag)
				resultCh <- Result{Videos: videos, Err: err, Tag: task.Hashtag}
			}
		}(i + 1)
	}

	// 等待所有 worker 完成后关闭结果 channel
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// 全局超时：每个标签最多 90 秒，超时后强制返回已有结果
	timeout := time.Duration(len(m.hashtags)) * 90 * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var allVideos []Video
	remaining := len(m.hashtags)
	for remaining > 0 {
		select {
		case res, ok := <-resultCh:
			if !ok {
				remaining = 0
				break
			}
			remaining--
			if res.Err != nil {
				log.Printf("[monitor] 标签 %s 抓取失败: %v", res.Tag, res.Err)
			} else {
				log.Printf("[monitor] 标签 %s 抓取完成，获得 %d 条视频", res.Tag, len(res.Videos))
				allVideos = append(allVideos, res.Videos...)
			}
		case <-timer.C:
			log.Printf("[monitor] 全局超时（%v），强制返回已有 %d 条视频", timeout, len(allVideos))
			remaining = 0
		}
	}

	// 按 GrowthScore 过滤
	if m.minScore > 0 {
		filtered := allVideos[:0]
		for _, v := range allVideos {
			if v.GrowthScore >= m.minScore {
				filtered = append(filtered, v)
			}
		}
		allVideos = filtered
	}

	// 按 GrowthScore 降序排列，潜力爆款排前面
	sort.Slice(allVideos, func(i, j int) bool {
		return allVideos[i].GrowthScore > allVideos[j].GrowthScore
	})

	log.Printf("[monitor] 监控完成，共 %d 条有效视频", len(allVideos))
	return allVideos, nil
}
