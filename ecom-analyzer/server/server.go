package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ecom-analyzer/ai"
	"ecom-analyzer/scraper"
)

// ScrapeJob 一次抓取任务的状态
type ScrapeJob struct {
	ID        string              `json:"id"`
	Keyword   string              `json:"keyword"`
	Platforms []string            `json:"platforms"`
	Rules     scraper.ScrapeRules `json:"rules"`
	Status    string              `json:"status"` // pending | running | done | error
	Progress  string              `json:"progress"`
	Products  []scraper.Product   `json:"products"`
	SavedFile string              `json:"saved_file,omitempty"`
	Error     string              `json:"error,omitempty"`
	CreatedAt time.Time           `json:"created_at"`
}

var (
	jobs   = map[string]*ScrapeJob{}
	jobsMu sync.RWMutex
)

func Run(addr string) {
	mux := http.NewServeMux()

	// 静态页面
	mux.HandleFunc("/", serveIndex)

	// API
	mux.HandleFunc("/api/scrape", handleScrape)
	mux.HandleFunc("/api/job/", handleJobStatus)
	mux.HandleFunc("/api/save", handleSave)
	mux.HandleFunc("/api/analyze", handleAnalyze)
	mux.HandleFunc("/api/files", handleListFiles)
	mux.HandleFunc("/api/load", handleLoadFile)
	mux.HandleFunc("/api/merge", handleMergeFiles)

	log.Printf("🌐 Web 界面已启动: http://%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	// 读取同目录下的 index.html
	dir := filepath.Dir(os.Args[0])
	// 开发时从源码目录读
	candidates := []string{
		"server/static/index.html",
		filepath.Join(dir, "static/index.html"),
	}
	for _, p := range candidates {
		if data, err := os.ReadFile(p); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(data)
			return
		}
	}
	http.Error(w, "index.html not found", 500)
}

// POST /api/scrape  body: {keyword, platforms:[amazon,tiktok], rules:{...}}
func handleScrape(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Keyword   string              `json:"keyword"`
		Platforms []string            `json:"platforms"`
		Rules     scraper.ScrapeRules `json:"rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Keyword == "" {
		jsonErr(w, "参数错误: keyword 必填", 400)
		return
	}
	// 补默认值
	if req.Rules.MaxItems <= 0 {
		req.Rules.MaxItems = 10
	}

	jobID := fmt.Sprintf("job_%d", time.Now().UnixMilli())
	job := &ScrapeJob{
		ID:        jobID,
		Keyword:   req.Keyword,
		Platforms: req.Platforms,
		Rules:     req.Rules,
		Status:    "pending",
		CreatedAt: time.Now(),
	}
	jobsMu.Lock()
	jobs[jobID] = job
	jobsMu.Unlock()

	go runScrapeJob(job)
	jsonOK(w, map[string]string{"job_id": jobID})
}

func runScrapeJob(job *ScrapeJob) {
	setJobStatus(job, "running", "正在启动浏览器...")

	products, err := scraper.ScrapeWithPlatforms(job.Keyword, job.Platforms, func(msg string) {
		setJobStatus(job, "running", msg)
	}, job.Rules)

	jobsMu.Lock()
	defer jobsMu.Unlock()
	if err != nil {
		job.Status = "error"
		job.Error = err.Error()
		return
	}
	job.Status = "done"
	job.Products = products
	job.Progress = fmt.Sprintf("完成，共 %d 个商品", len(products))

	// 自动保存到本地文件
	if len(products) > 0 {
		os.MkdirAll("data", 0755)
		filename := fmt.Sprintf("%s_%s.json",
			sanitizeFilename(job.Keyword),
			job.CreatedAt.Format("20060102_150405"),
		)
		path := filepath.Join("data", filename)
		if data, err := json.MarshalIndent(products, "", "  "); err == nil {
			if err = os.WriteFile(path, data, 0644); err == nil {
				job.SavedFile = filename
				log.Printf("✅ 自动保存: %s (%d 个商品)", path, len(products))
			}
		}
	}
}

func sanitizeFilename(s string) string {
	r := strings.NewReplacer(" ", "_", "/", "-", "\\", "-", ":", "-", "*", "-", "?", "-", "\"", "-", "<", "-", ">", "-", "|", "-")
	return r.Replace(s)
}

func setJobStatus(job *ScrapeJob, status, progress string) {
	jobsMu.Lock()
	job.Status = status
	job.Progress = progress
	jobsMu.Unlock()
}

// GET /api/job/{id}
func handleJobStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/job/")
	jobsMu.RLock()
	job, ok := jobs[id]
	jobsMu.RUnlock()
	if !ok {
		jsonErr(w, "job not found", 404)
		return
	}
	jsonOK(w, job)
}

// POST /api/save  body: {filename, products:[...]}
func handleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Filename string            `json:"filename"`
		Products []scraper.Product `json:"products"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "参数错误", 400)
		return
	}
	if req.Filename == "" {
		req.Filename = fmt.Sprintf("scrape_%s.json", time.Now().Format("20060102_150405"))
	}
	// 保存到 data/ 目录
	os.MkdirAll("data", 0755)
	path := filepath.Join("data", req.Filename)
	data, _ := json.MarshalIndent(req.Products, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		jsonErr(w, "保存失败: "+err.Error(), 500)
		return
	}
	jsonOK(w, map[string]string{"path": path})
}

// GET /api/files  列出 data/ 目录下的 json 文件
func handleListFiles(w http.ResponseWriter, r *http.Request) {
	os.MkdirAll("data", 0755)
	entries, err := os.ReadDir("data")
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	var files []map[string]any
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			info, _ := e.Info()
			files = append(files, map[string]any{
				"name":     e.Name(),
				"size":     info.Size(),
				"modified": info.ModTime().Format("2006-01-02 15:04:05"),
			})
		}
	}
	jsonOK(w, files)
}

// GET /api/load?file=xxx.json
func handleLoadFile(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("file")
	if name == "" || strings.Contains(name, "..") {
		jsonErr(w, "invalid filename", 400)
		return
	}
	data, err := os.ReadFile(filepath.Join("data", name))
	if err != nil {
		jsonErr(w, "文件不存在", 404)
		return
	}
	var products []scraper.Product
	if err = json.Unmarshal(data, &products); err != nil {
		jsonErr(w, "JSON 解析失败", 500)
		return
	}
	jsonOK(w, products)
}

// POST /api/merge  body: {files:["a.json","b.json"], output:"merged.json"}
func handleMergeFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Files    []string `json:"files"`
		Output   string   `json:"output"`
		Dedupe   bool     `json:"dedupe"` // 按 URL 去重
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Files) == 0 {
		jsonErr(w, "参数错误: files 必填", 400)
		return
	}

	seen := map[string]bool{}
	var merged []scraper.Product

	for _, name := range req.Files {
		if strings.Contains(name, "..") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("data", name))
		if err != nil {
			continue
		}
		var products []scraper.Product
		if err = json.Unmarshal(data, &products); err != nil {
			continue
		}
		for _, p := range products {
			key := p.URL
			if key == "" {
				key = p.Title
			}
			if req.Dedupe && seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, p)
		}
	}

	if req.Output == "" {
		req.Output = fmt.Sprintf("merged_%s.json", time.Now().Format("20060102_150405"))
	}
	os.MkdirAll("data", 0755)
	path := filepath.Join("data", req.Output)
	data, _ := json.MarshalIndent(merged, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		jsonErr(w, "保存失败: "+err.Error(), 500)
		return
	}
	jsonOK(w, map[string]any{"path": path, "count": len(merged), "filename": req.Output})
}

// POST /api/analyze  body: {products:[...], prompt_template, api_key, provider}
func handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Products       []scraper.Product `json:"products"`
		PromptTemplate string            `json:"prompt_template"`
		APIKey         string            `json:"api_key"`
		Provider       string            `json:"provider"` // gemini | openai
		Endpoint       string            `json:"endpoint"` // 自定义 API 端点
		Keyword        string            `json:"keyword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "参数错误", 400)
		return
	}

	// 把 key 写入环境变量供 callGemini/callOpenAI 兜底使用
	switch req.Provider {
	case "openai":
		if req.APIKey != "" {
			os.Setenv("OPENAI_API_KEY", req.APIKey)
		}
	default:
		if req.APIKey != "" {
			os.Setenv("GEMINI_API_KEY", req.APIKey)
		}
	}

	// 如果用户填了自定义端点，把 key 拼进去（Gemini URL 参数形式）或留给 callEndpoint 处理
	endpoint := req.Endpoint
	if endpoint != "" && req.APIKey != "" && strings.Contains(endpoint, "generativelanguage.googleapis.com") {
		if !strings.Contains(endpoint, "key=") {
			sep := "?"
			if strings.Contains(endpoint, "?") {
				sep = "&"
			}
			endpoint = endpoint + sep + "key=" + req.APIKey
		}
	}

	report, err := ai.AnalyzeWithTemplate(req.Keyword, req.Products, req.PromptTemplate, endpoint)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	jsonOK(w, report)
}

func jsonOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}
