package tiktok

import (
	"math"
	"time"
)

const (
	// decayHalfLife 时间衰减半衰期（小时），7天内越新权重越高
	decayHalfLife = 72.0
)

// CalcGrowthScore 计算增长分数
//
// 公式: (likes + comments + shares) / plays * timeDecay
//
// timeDecay = e^(-ln2 / halfLife * hoursAgo)，越新的视频衰减越小（权重越高）
func CalcGrowthScore(v Video) float64 {
	if v.PlayCount == 0 {
		return 0
	}
	engagement := float64(v.LikeCount + v.CommentCount + v.ShareCount)
	engagementRate := engagement / float64(v.PlayCount)

	hoursAgo := time.Since(v.PublishedAt).Hours()
	if hoursAgo < 0 {
		hoursAgo = 0
	}
	decay := math.Exp(-math.Log(2) / decayHalfLife * hoursAgo)

	return engagementRate * decay
}
