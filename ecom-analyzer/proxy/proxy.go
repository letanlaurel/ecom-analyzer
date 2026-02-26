package proxy

import (
	"math/rand"
	"time"
)

// 代理列表，实际使用时替换为真实代理
var proxyList = []string{
	// "http://user:pass@proxy1.example.com:8080",
	// "http://user:pass@proxy2.example.com:8080",
}

// UserAgent 池，模拟真实浏览器
var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:122.0) Gecko/20100101 Firefox/122.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_2_1) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
}

// GetRandomProxy 随机返回一个代理，无代理时返回空字符串
func GetRandomProxy() string {
	if len(proxyList) == 0 {
		return ""
	}
	return proxyList[rand.Intn(len(proxyList))]
}

// GetRandomUserAgent 随机返回一个 User-Agent
func GetRandomUserAgent() string {
	return userAgents[rand.Intn(len(userAgents))]
}

// RandomSleep 随机等待 min~max 秒，模拟人工操作
func RandomSleep(minSec, maxSec int) {
	n := minSec + rand.Intn(maxSec-minSec+1)
	time.Sleep(time.Duration(n) * time.Second)
}
