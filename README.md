# E-Commerce Analyzer

跨境电商选品分析工具，支持多平台商品数据抓取与 AI 智能分析。

## 功能特性

- **多平台抓取**：支持 Amazon、TikTok Shop、eBay、Etsy、Walmart、AliExpress、淘宝、京东、拼多多
- **AI 选品分析**：基于 Gemini / OpenAI 的智能选品建议，评估技术门槛、社交传播力、利润率
- **TikTok 趋势监控**：实时监控热门标签，发现潜力爆款
- **Web 界面**：可视化操作界面，支持任务管理、数据合并、报告导出

## 安装

### 前置要求

- Go 1.25+
- Playwright 浏览器（首次运行时自动安装）

```bash
cd ecom-analyzer
go mod download
go run github.com/playwright-community/playwright-go install-browsers
```

### 配置环境变量

```bash
cp .env.example .env
# 编辑 .env 填入 API Key
```

## 快速开始

### 命令行模式

```bash
# 抓取并分析关键词
go run main.go "Smart Pet Feeder"
```

### Web 界面模式

```bash
go run ./cmd/web
# 访问 http://localhost:8080
```

### TikTok 趋势监控

```bash
go run ./cmd/tiktok_monitor
```

## 环境变量说明

| 变量名 | 说明 | 必填 |
|--------|------|------|
| `GEMINI_API_KEY` | Google Gemini API Key | 二选一 |
| `OPENAI_API_KEY` | OpenAI API Key | 二选一 |
| `EBAY_APP_ID` / `EBAY_CERT_ID` | eBay Browse API | 可选 |
| `ETSY_API_KEY` | Etsy Open API v3 | 可选 |
| `TAOBAO_APP_KEY` / `TAOBAO_APP_SECRET` | 淘宝客 API | 可选 |
| `JD_APP_KEY` / `JD_APP_SECRET` | 京东联盟 API | 可选 |
| `PDD_CLIENT_ID` / `PDD_CLIENT_SECRET` | 拼多多 API | 可选 |
| `TIKTOK_HASHTAGS` | TikTok 监控标签（逗号分隔） | 可选 |
| `HTTP_PROXY` | 代理地址 | 可选 |

## 项目结构

```
ecom-analyzer/
├── main.go              # 命令行入口
├── ai/
│   └── analyzer.go      # AI 分析模块
├── scraper/
│   └── scraper.go       # 多平台抓取器
├── tiktok/
│   ├── monitor.go       # TikTok 监控调度
│   ├── scraper.go       # TikTok 抓取实现
│   └── scorer.go        # 增长分数计算
├── server/
│   └── server.go        # Web API 服务
├── cmd/
│   ├── web/             # Web 服务入口
│   └── tiktok_monitor/  # TikTok 监控入口
└── data/                # 抓取数据存储
```

## API 接口

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/scrape` | POST | 启动抓取任务 |
| `/api/job/{id}` | GET | 查询任务状态 |
| `/api/analyze` | POST | AI 分析商品数据 |
| `/api/files` | GET | 列出已保存文件 |
| `/api/merge` | POST | 合并多个数据文件 |
| `/api/tiktok/monitor` | POST | 启动 TikTok 监控 |

## AI 分析报告结构

```json
{
  "keyword": "Smart Pet Feeder",
  "market_overview": "市场整体判断",
  "top_products": [
    {
      "title": "商品标题",
      "tech_barrier": 3,
      "social_viral_score": 8,
      "profit_margin_score": 6,
      "worth_following": true
    }
  ],
  "opportunities": ["机会点"],
  "risks": ["风险点"],
  "recommendation": "综合建议"
}
```

## TikTok 增长分数算法

```
GrowthScore = (互动量 / 播放量) × 时间衰减因子
时间衰减 = e^(-ln2 / 72h × 发布时长)
```

越新的视频权重越高，互动率高的内容排名靠前。

## License

MIT
