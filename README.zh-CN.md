# ai-gateway

> English: [README.md](README.md)

自托管、OpenAI 兼容的 **AI 流量控制平面**，Go 编写。一个二进制，把虚拟 Key、配额、审计日志、Token 统计、成本核算与多供应商负载均衡放在你的应用与所有大模型之间。

**文档：** [产品愿景](docs/zh-CN/01-product-vision.md) · [差距分析](docs/zh-CN/02-gap-analysis.md) · [路线图](docs/zh-CN/03-roadmap.md) · [设计套件](docs/zh-CN/README.md)

## 功能

- **虚拟 Key 管理** —— 发放 `sk-vk-*` 凭证（AES-256-GCM 加密存储、SHA-256 查找）；轮换上游 Key 无需改动客户端
- **多供应商路由** —— 四种按 Key 路由策略（加权/优先级/最低延迟/最低成本）、自动重试/故障转移、映射级降级链与逐次尝试审计、多实例共享的 Redis 熔断器（[设计](docs/zh-CN/design/01-routing-and-lb.md)）
- **协议适配** —— OpenAI 兼容客户端可原生调用 Anthropic、Gemini（完整请求/响应/SSE 翻译）与 Azure OpenAI，用量统一归一化（[设计](docs/zh-CN/design/02-protocol-adapters.md)）
- **多租户** —— 租户→项目→Key 层级，零配置默认租户；按租户/项目/Key/模型的成本归属（[设计](docs/zh-CN/design/04-multi-tenancy-and-auth.md)）
- **余额计费** —— 按租户可选启用的预付/后付账户、复式流水、代理路径冻结→结算扣减、宽限期停用、预算告警、与上游成本解耦的售价价格表（[设计](docs/zh-CN/design/03-billing-and-monetization.md)）
- **多维配额** —— 日/小时 Token、请求数、并发槽、积分预算；按模型覆盖；Redis Lua 原子执行
- **Token 统计与报表** —— 从每个响应解析用量（含流式与缓存 Token），按模型计价，按日聚合支撑看板与分摊
- **PII 护栏** —— 规则式检测引擎（身份证含校验位、手机号、银行卡 Luhn、邮箱、API 密钥）+ 提示注入签名；按策略 block / redact / log（[设计](docs/zh-CN/design/06-security-and-guardrails.md)）
- **响应缓存** —— 归一化键的精确缓存、合成流式回放、可配置命中计费（free/discount/full）（[设计](docs/zh-CN/design/07-caching-strategies.md)）
- **审计日志** —— 每个请求全记录（Token、延迟、PII 动作、客户端元数据），异步批量落库，可选 Elasticsearch 索引，会话聚合
- **可观测性** —— 独立端口的 Prometheus `/metrics`、`/healthz` + `/readyz` 探针、仓库自带 Grafana 看板（[设计](docs/zh-CN/design/05-observability.md)）
- **Web 控制台** —— 内嵌于二进制的 React SPA（`/console/`）：仪表盘（含用量）、Key、提供方（实时熔断）、审计、租户、计费；源码维护于 [`frontend/`](frontend/)
- **管理面认证** —— 静态管理令牌（`AIGW_ADMIN_TOKEN`）保护全部 `/ai/gateway/*` 端点
- **多数据库** —— MySQL（默认）、PostgreSQL、SQLite（演示）；另有会话亲和、模型映射、IP 白名单、L1/L2 Key 缓存

## 仓库结构

```text
├── backend/    # Go 网关（Kratos）：代理、配额、审计、路由、控制台内嵌
├── frontend/   # React + TypeScript Web 控制台（Vite）
├── docs/       # 产品与设计文档（英文 + 中文）
├── deploy/     # docker-compose 栈、Prometheus 与 Grafana 配置
└── Dockerfile  # 多阶段：控制台构建 → Go 构建 → 运行镜像
```

## 快速开始

### docker compose（推荐）

```bash
git clone https://github.com/opscenter/ai-gateway.git
cd ai-gateway/deploy/compose
docker compose up -d
# 含 Prometheus + Grafana：
docker compose --profile observability up -d
```

网关监听 `:8080`（代理 + 管理 + 控制台）与 `:9090`（指标）。**正式使用前务必修改 `docker-compose.yml` 中的 `AIGW_ADMIN_TOKEN` 与 `AIGW_ENCRYPTION_KEY`。**

### 源码构建

```bash
# 仅后端（控制台显示占位页）
cd backend && go build -o server ./cmd/server && ./server -conf configs/config.yaml

# 完整构建：控制台 + 内嵌 + 服务端
make all && make run
```

### 第一个请求

```bash
ADMIN="Authorization: Bearer change-this-admin-token"

# 1. 注册上游提供方
curl -X POST localhost:8080/ai/gateway/providers -H "$ADMIN" -H 'Content-Type: application/json' -d '{
  "name": "openai", "baseUrl": "https://api.openai.com/v1",
  "apiKey": "sk-your-upstream-key",
  "models": [{"name": "gpt-4o-mini", "is_default": true}]
}'

# 2. 创建虚拟 Key（响应包含 sk-vk-* 明文 —— 仅展示一次）
curl -X POST localhost:8080/ai/gateway/key -H "$ADMIN" -H 'Content-Type: application/json' \
  -d '{"name": "demo", "providerId": 1}'

# 3. 像调用 OpenAI 一样调用
curl localhost:8080/ai/v1/chat/completions \
  -H "Authorization: Bearer sk-vk-..." -H 'Content-Type: application/json' \
  -d '{"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "你好"}]}'
```

打开 `http://localhost:8080/console/`，用管理令牌登录控制台。

## 配置

`backend/configs/config.yaml`，所有键均可用环境变量覆盖：

| 环境变量 | 用途 |
| --- | --- |
| `AIGW_HTTP_ADDR` / `AIGW_METRICS_ADDR` | 监听地址（默认 `:8080` / `:9090`） |
| `AIGW_DB_DRIVER` / `AIGW_DB_DSN` | `mysql`（默认）、`postgres`、`sqlite` |
| `AIGW_REDIS_ADDR` / `AIGW_REDIS_PASSWORD` | Redis |
| `AIGW_ENCRYPTION_KEY` | **精确 32 字节** —— 加密虚拟 Key 与提供方 Key |
| `AIGW_ADMIN_TOKEN` | 管理 API 令牌；为空 = 无认证（仅限开发，会打警告） |

数据表在启动时自动创建（GORM 加法式自动迁移）。

> **本地开发：** 不要把真实凭证提交进 git——将 `config.yaml` 复制为 `configs/config.local.yaml`（已 gitignore）后用 `./server -conf configs/config.local.yaml` 启动，或使用 `AIGW_*` 环境变量。

## API 面

- **代理**（`Authorization: Bearer sk-vk-*`）：`GET /ai/v1/models`、`POST /ai/v1/chat/completions`、`POST /ai/v1/embeddings`、`POST /ai/v1/rerank`，其余 `/ai/v1/*` 透传。OpenAI 兼容，长期承诺不做破坏性变更。`providerType: anthropic` / `gemini` / `azure_openai` 的提供方透明翻译。
- **管理**（`Authorization: Bearer <管理令牌>`）：
  - Key：CRUD 与明文取回、配额配置/用量
  - 提供方：CRUD 与 `GET /ai/gateway/providers/health`（实时熔断状态）
  - 租户：`POST|GET /ai/gateway/tenants`、`POST|GET /ai/gateway/projects`
  - 计费：`POST /ai/gateway/billing/recharge`、`PUT /ai/gateway/billing/account`、`GET /ai/gateway/billing/ledger`
  - 报表：`GET /ai/gateway/stats/overview`、`GET /ai/gateway/stats/timeseries`
  - 审计：列表 / 会话 / 安全总览
- **运维**（免认证）：`GET /healthz`、`GET /readyz`；指标端口上的 `GET /metrics`。

## 状态与路线图

对照[路线图](docs/zh-CN/03-roadmap.md)已实现：

- **P0 开源就绪**：加权 LB + 故障转移 + 熔断、指标/探针、管理认证、提供方 Key 加密、多数据库、测试 + CI、compose 栈、控制台 MVP。
- **P1 商业闭环（核心）**：租户→项目→Key 层级、可选启用的预付/后付余额账户（复式流水 + 冻结→结算扣减）、宽限期停用、预算告警、售价价格表、按日用量归集与报表、规则式 PII 引擎（block/redact/log）。
- **P2 差异化（核心）**：Anthropic 与 Gemini 原生出口适配（含 SSE 流式翻译）、Azure OpenAI 适配及用量归一化、精确响应缓存与缓存感知计费。
- **补缺一轮**：路由策略 + 降级链 + 延迟 EWMA、提供方模型同步、项目配额模板、计费告警 webhook、`doctor`/`rekey` CLI、OpenAPI 规范、Helm chart、CI 覆盖率门槛 + PostgreSQL 冒烟、控制台管理 UI（Key 创建含一次性明文、提供方表单、用量图表）。

### 尚未实现（均已有完整设计，见[设计套件](docs/zh-CN/README.md)）

| 领域 | 缺失部分 |
| --- | --- |
| 访问控制（[D04](docs/zh-CN/design/04-multi-tenancy-and-auth.md)） | SSO/OIDC（用户体系按计划跳过——后续直接引入 SSO）、管理查询的租户级隔离 |
| 路由（[D01](docs/zh-CN/design/01-routing-and-lb.md)） | 主动健康探测（当前仅被动熔断） |
| 计费商业化（[D03](docs/zh-CN/design/03-billing-and-monetization.md)） | 支付网关（Stripe/支付宝/微信）、订阅套餐、发票、邮件告警通道 |
| 协议（[D02](docs/zh-CN/design/02-protocol-adapters.md)） | Anthropic Messages 与 Responses API 入口、Bedrock 出口适配（SigV4）、Batch 与 Files API 代理 |
| 安全（[D06](docs/zh-CN/design/06-security-and-guardrails.md)） | 可插拔护栏 checker 链、外部 PII 引擎（gRPC/Presidio）、出向流扫描、审计正文加密 |
| 缓存（[D07](docs/zh-CN/design/07-caching-strategies.md)） | 语义缓存（向量后端）、流式响应缓存、缓存清空 API |
| 可观测性（[D05](docs/zh-CN/design/05-observability.md)） | OpenTelemetry 追踪 |
| 控制台（[D08](docs/zh-CN/design/08-web-console.md)） | 模型与价格表页面、审计正文/会话视图、系统设置、Playwright E2E |
| 面向未来（[D09](docs/zh-CN/design/09-extensibility.md)） | 插件/Hook 机制、事件总线、MCP 网关 |

欢迎贡献——每一行背后都有完整的技术设计。

## 开发

```bash
cd backend
go test ./...        # 单元测试（miniredis + 内存 SQLite，无需外部服务）
go vet ./...
wire ./cmd/server    # 修改 ProviderSet 后重新生成 DI

cd ../frontend
npm run dev          # 控制台开发服务器 :5173，/ai 代理到 :8080
```

贡献约定见 [CONTRIBUTING.md](CONTRIBUTING.md)，漏洞报告见 [SECURITY.md](SECURITY.md)。

## 许可证

[MIT](LICENSE)
