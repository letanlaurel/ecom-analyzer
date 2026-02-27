package tiktok

import "time"

// Video 代表一条 TikTok 视频的抓取结果
type Video struct {
	URL          string    `json:"url"`
	Author       string    `json:"author,omitempty"`
	PublishedAt  time.Time `json:"published_at"`
	PlayCount    int64     `json:"play_count"`
	LikeCount    int64     `json:"like_count"`
	CommentCount int64     `json:"comment_count"`
	ShareCount   int64     `json:"share_count"`
	Hashtag      string    `json:"hashtag"`
	GrowthScore  float64   `json:"growth_score"`
}

// Task 代表一个待抓取的标签任务
type Task struct {
	Hashtag string
}

// Result 爬虫协程返回的结果（含错误）
type Result struct {
	Videos []Video
	Err    error
	Tag    string
}
