# ai-gateway

> English: [README.md](README.md)

自托管、OpenAI 兼容的 **AI 流量控制平面**，Go 编写。一个二进制，把虚拟 Key、配额、审计日志、Token 统计、成本核算与多供应商负载均衡放在你的应用与所有大模型之间。

**文档：** [产品愿景](docs/zh-CN/01-product-vision.md) · [差距分析](docs/zh-CN/02-gap-analysis.md) · [路线图](docs/zh-CN/03-roadmap.md) · [设计套件](docs/zh-CN/README.md)

## 功能

- **虚拟 Key 管理** —— 发放 `sk-vk-*` 凭证（AES-256-GCM 加密存储、SHA-256 查找）；轮换上游 Key 无需改动客户端
- **多供应商路由** —— 加权负载均衡、跨提供方自动重试/故障转移、多实例共享的 Redis 熔断器（[设计](docs/zh-CN/design/01-routing-and-lb.md)）
- **多维配额** —— 日/小时 Token、请求数、并发槽、积分预算；按模型覆盖；Redis Lua 原子执行
- **Token 统计与成本** —— 从每个响应解析用量（含流式与缓存 Token），按模型计价并折算积分
- **审计日志** —— 每个请求全记录（Token、延迟、PII 动作、客户端元数据），异步批量落库，可选 Elasticsearch 索引，会话聚合
- **可观测性** —— 独立端口的 Prometheus `/metrics`、`/healthz` + `/readyz` 探针、仓库自带 Grafana 看板（[设计](docs/zh-CN/design/05-observability.md)）
- **Web 控制台** —— 内嵌于二进制的 React SPA（`/console/`）：仪表盘、Key、提供方（实时熔断状态）、审计；源码独立维护于 [`frontend/`](frontend/)
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

## API 面

- **代理**（`Authorization: Bearer sk-vk-*`）：`GET /ai/v1/models`、`POST /ai/v1/chat/completions`、`POST /ai/v1/embeddings`、`POST /ai/v1/rerank`，其余 `/ai/v1/*` 透传。OpenAI 兼容，长期承诺不做破坏性变更。
- **管理**（`Authorization: Bearer <管理令牌>`）：虚拟 Key CRUD 与明文取回、配额配置/用量、审计列表/会话/安全总览、提供方 CRUD 与 `GET /ai/gateway/providers/health`（实时熔断状态）。
- **运维**（免认证）：`GET /healthz`、`GET /readyz`；指标端口上的 `GET /metrics`。

## 状态与路线图

当前版本实现了[路线图](docs/zh-CN/03-roadmap.md)的 **P0「开源就绪」**里程碑：加权 LB + 故障转移 + 熔断、指标/探针、管理认证、提供方 Key 加密、多数据库、测试 + CI、compose 栈、控制台 MVP。P1（多租户、余额计费、预算告警）与 P2（Anthropic/Gemini 原生协议、语义缓存、护栏、支付）已在[设计套件](docs/zh-CN/README.md)中完成设计——欢迎贡献。

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
