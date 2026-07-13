# ai-gateway

> English: [README.md](README.md)

自托管、OpenAI 兼容的 **AI 流量控制平面**，Go 编写。一个二进制，把虚拟 Key、配额、审计日志、Token 统计、成本核算与多供应商负载均衡放在你的应用与所有大模型之间。

**文档：** [产品愿景](docs/zh-CN/01-product-vision.md) · [差距分析](docs/zh-CN/02-gap-analysis.md) · [路线图](docs/zh-CN/03-roadmap.md) · [设计套件](docs/zh-CN/README.md)

## 功能

- **虚拟 Key 管理** —— 发放 `sk-vk-*` 凭证（AES-256-GCM 加密存储、SHA-256 查找）；轮换上游 Key 无需改动客户端
- **多供应商路由** —— 四种按 Key 路由策略（加权/优先级/最低延迟/最低成本）、自动重试/故障转移、映射级降级链与逐次尝试审计、多实例共享的 Redis 熔断器、可选的主动健康探测（用于空闲期熔断恢复）（[设计](docs/zh-CN/design/01-routing-and-lb.md)）
- **协议适配** —— 出向：Anthropic、Gemini、Azure OpenAI、Bedrock（Claude/Titan/Llama/Mistral/Nova，手写 SigV4）；入向：OpenAI Chat、Anthropic Messages（`/anthropic/v1/messages`）、Responses API（`/ai/v1/responses`，支持 `previous_response_id`/`store` 服务端会话状态）；双向完整 SSE 流式翻译（[设计](docs/zh-CN/design/02-protocol-adapters.md)）
- **多租户** —— 租户→项目→Key 层级，零配置默认租户，项目配额模板；按租户/项目/Key/模型的成本归属（[设计](docs/zh-CN/design/04-multi-tenancy-and-auth.md)）
- **认证与访问控制** —— 启动管理令牌、OIDC/SSO 登录（JIT 用户开通 + claim→角色映射）、四角色 RBAC（owner/admin/member/viewer）、管理员 API Key、操作审计日志（[设计](docs/zh-CN/design/04-multi-tenancy-and-auth.md)）
- **余额计费** —— 按租户可选启用的预付/后付账户、复式流水、代理路径冻结→结算扣减、宽限期停用、预算告警（webhook）、与上游成本解耦的售价价格表（[设计](docs/zh-CN/design/03-billing-and-monetization.md)）
- **多维配额** —— 日/小时 Token、请求数、并发槽、积分预算、MCP 工具调用配额；按模型覆盖；Redis Lua 原子执行
- **Token 统计与报表** —— 从每个响应解析用量（含流式与缓存 Token），按模型计价，按日聚合支撑看板与分摊
- **护栏** —— 每策略可插拔 checker 链（规则式 PII 检测、提示注入签名、话题围栏、外部 gRPC checker），block / redact / log，非流式与流式出向扫描均支持（流式为尽力而为）、审计正文 AES-GCM 加密（[设计](docs/zh-CN/design/06-security-and-guardrails.md)）
- **响应缓存** —— 精确缓存 + 语义（嵌入相似度）缓存，向量后端可插拔（Redis/RediSearch）、合成流式回放、可配置命中计费（free/discount/full）（[设计](docs/zh-CN/design/07-caching-strategies.md)）
- **MCP 网关** —— 在与模型相同的 `sk-vk-*` 虚拟 Key 之下代理 Streamable HTTP MCP 工具流量（批量 JSON-RPC、GET/SSE 推送、POST），支持按 Key 工具白名单与参数/结果护栏扫描（[设计](docs/zh-CN/design/09-extensibility.md)）
- **Batch + Files API 代理** —— 面向 `openai_compatible` provider 的透传，含影子记账与按上游批量折扣的延迟用量结算
- **可扩展性** —— `pre_request`/`post_response` Hook（编译期、webhook 或通过 `wazero` 的 WASM）与事件总线（`on_audit`/`on_billing`），持久化日志 + webhook/Kafka sink（[设计](docs/zh-CN/design/09-extensibility.md)）
- **审计日志** —— 每个请求全记录（Token、延迟、护栏动作、客户端元数据），异步批量落库，可选 Elasticsearch 索引，会话聚合
- **可观测性** —— 独立端口的 Prometheus `/metrics`（含 Go 运行时/进程/数据库连接池指标）、`/healthz` + `/readyz` 探针、仓库自带 Grafana 看板、可选启用的 OpenTelemetry 追踪（[设计](docs/zh-CN/design/05-observability.md)）
- **Web 控制台** —— 内嵌于二进制的 React SPA（`/console/`）：仪表盘/用量图表、Key、提供方（实时熔断）、模型映射（降级链拖拽编辑器）、护栏策略（checker 链编排）、审计（正文/会话/安全视图）、租户、计费、用户与管理员 Key、系统设置；源码维护于 [`frontend/`](frontend/)
- **公开首页** —— 静态 HTML/CSS/JS 营销页（无构建步骤），以与控制台相同的内嵌方式服务于 `/`；源码维护于 [`homepage/`](homepage/)
- **多数据库** —— MySQL（默认）、PostgreSQL、SQLite（演示）；另有会话亲和、模型映射、IP 白名单、L1/L2 Key 缓存

## 仓库结构

```text
├── backend/    # Go 网关（Kratos）：代理、配额、审计、路由、控制台内嵌
├── frontend/   # React + TypeScript Web 控制台（Vite）
├── homepage/   # 公开营销页，纯 HTML/CSS/JS，无构建步骤 —— 服务于 "/"
├── docs/       # 产品与设计文档（英文 + 中文）
├── deploy/     # docker-compose 栈、Prometheus 与 Grafana 配置
└── Dockerfile  # 多阶段：控制台构建 → Go 构建 → 运行镜像
```

## 快速开始

### docker compose（推荐）

```bash
git clone https://github.com/adcwb/ai-gateway.git
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

- **代理**（`Authorization: Bearer sk-vk-*`）：`GET /ai/v1/models`、`POST /ai/v1/chat/completions`、`POST /ai/v1/embeddings`、`POST /ai/v1/rerank`、`POST /ai/v1/responses`，其余 `/ai/v1/*` 及 Batch/Files 端点透传。OpenAI 兼容，长期承诺不做破坏性变更。`providerType: anthropic` / `gemini` / `azure_openai` / `bedrock` 的提供方透明翻译。`POST /anthropic/v1/messages` 接受原生 Anthropic 请求。`/ai/mcp/{serverName}` 在相同虚拟 Key 之下代理 MCP 工具调用流量。
- **管理**（`Authorization: Bearer <管理令牌>`，或受 RBAC 约束的管理员 API Key / SSO 会话）：
  - Key：CRUD 与明文取回、配额配置/用量、按 Key 缓存配置、PII 策略绑定、工具白名单
  - 提供方：CRUD、`GET /ai/gateway/providers/health`（实时熔断状态）、模型同步
  - 模型映射与护栏策略：CRUD（降级链、checker 链）
  - 租户：`POST|GET /ai/gateway/tenants`、`POST|GET /ai/gateway/projects`（配额模板）
  - 计费：`POST /ai/gateway/billing/recharge`、`PUT /ai/gateway/billing/account`、`GET /ai/gateway/billing/ledger`
  - 报表：`GET /ai/gateway/stats/overview`、`GET /ai/gateway/stats/timeseries`
  - 审计：列表 / 会话 / 安全总览
  - 用户、管理员 API Key、SSO/OIDC 配置、扩展（Hook/事件总线 sink）
- **运维**（免认证）：`GET /healthz`、`GET /readyz`；指标端口上的 `GET /metrics`。

## 状态与路线图

对照[路线图](docs/zh-CN/03-roadmap.md)：从 P0 到协议面、MCP 网关/可扩展性、公开首页各轮均已交付。逐项能力的"已完成 / 部分完成 / 仅设计"权威列表见 [`CLAUDE.md` 中的 Feature status 表](CLAUDE.md#feature-status-what-exists-vs-what-doesnt)（英文，随代码同步维护）。

### 尚未实现（均已有完整设计，见[设计套件](docs/zh-CN/README.md)）

| 领域 | 缺失部分 |
| --- | --- |
| 访问控制（[D04](docs/zh-CN/design/04-multi-tenancy-and-auth.md)） | 所有列表/查询类端点的租户级过滤（当前 RBAC 仅覆盖已命名的状态变更操作） |
| 协议（[D02](docs/zh-CN/design/02-protocol-adapters.md)） | 4 个较新 Bedrock 模型族的工具调用/多模态支持、provider `adapter_config`/bedrock 凭证的控制台 UI |
| 计费商业化（[D03](docs/zh-CN/design/03-billing-and-monetization.md)） | 支付网关（Stripe/支付宝/微信）、订阅套餐、发票、邮件告警通道 |
| 安全（[D06](docs/zh-CN/design/06-security-and-guardrails.md)） | LLM 裁判升级、`topic_fence` 的嵌入相似度模式 |
| 可扩展性（[D09](docs/zh-CN/design/09-extensibility.md)） | `ai_extensions` 与熔断/配额事件总线的控制台 UI；Anthropic Message Batches API 翻译（Batch/Files 代理仅支持 openai_compatible provider） |

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
