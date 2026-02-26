package scraper

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// StripHTML 移除所有 HTML 标签，返回纯文本
func StripHTML(raw string) string {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		// 降级：用正则兜底
		return stripHTMLRegex(raw)
	}
	var buf strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			buf.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return normalizeWhitespace(buf.String())
}

// stripHTMLRegex 正则兜底方案
func stripHTMLRegex(s string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	return normalizeWhitespace(re.ReplaceAllString(s, " "))
}

// normalizeWhitespace 合并多余空白字符
func normalizeWhitespace(s string) string {
	re := regexp.MustCompile(`\s+`)
	return strings.TrimSpace(re.ReplaceAllString(s, " "))
}
