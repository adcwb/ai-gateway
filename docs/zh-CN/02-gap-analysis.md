# 02 · 差距分析

> [English version](../02-gap-analysis.md) · [ai-gateway 文档套件](README.md)的一部分

本文诚实盘点 ai-gateway 今天能做什么，以及要服务[产品愿景](01-product-vision.md)中的三类用户画像还缺什么。以下所有"现状"结论均已对照代码库核实；文件引用指向真实实现。

**成熟度图例：** ✅ 完整 · 🟡 部分 · ⚪ 桩代码（框架在、无逻辑） · 🔴 缺失

## 现有能力盘点

| 能力 | 成熟度 | 代码证据 |
| --- | --- | --- |
| 虚拟 Key 生命周期（创建/轮换/停用/过期，AES-256-GCM 加密存储，SHA-256 查找） | ✅ | `internal/biz/gateway.go`、`internal/pkg/aes.go`、`internal/data/model/virtual_key.go` |
| 多维配额（日/小时 Token、请求数、积分、并发；按模型覆盖） | ✅ | `internal/biz/quota.go`（Redis Lua 滑动窗口）、`virtual_key_model_quota.go` |
| 审计日志（异步批处理，MySQL + 可选 ES，独立请求体表，会话聚合） | ✅ | `internal/biz/audit.go`（worker 池、spill 队列、重试） |
| OpenAI 兼容代理（chat、embeddings、rerank、透传） | ✅ | `internal/biz/gateway.go`、`internal/server/http.go` |
| 模型映射（精确 + 正则，提供方覆盖） | ✅ | `internal/data/model/model_mapping.go`、`matchModelMapping()` |
| 会话亲和（header / prompt_cache_key / 内容哈希） | ✅ | `internal/biz/sticky_session.go` |
| Key 解析缓存（L1 sync.Map + L2 Redis + pub/sub 失效） | ✅ | `internal/biz/key_cache.go` |
| IP 白名单 | ✅ | `internal/middleware/virtual_key_auth.go` |
| 成本核算（Token → CNY → 积分） | 🟡 | `internal/biz/credits.go` —— 只有计算；币种硬编码 CNY |
| 负载均衡 | 🟡 | `mrand.IntN` 随机挑选（`internal/biz/gateway.go:821,836`）；`AIProvider.Weight` 字段存在但**从未被读取** |
| 提供方健康 | 🔴 | `AIProvider.IsHealthy()` 原样返回 `IsEnabled`（`internal/data/model/provider.go:35`）—— 无探测、无熔断 |
| 故障转移 / 跨提供方重试 | 🔴 | 代理路径上游失败无任何重试逻辑 |
| PII 检测 | ⚪ | `internal/biz/pii.go` —— 永远放行；动作框架（block/redact/log）已就位 |
| 指标 / 追踪 / 健康端点 | 🔴 | 无 Prometheus、无 OpenTelemetry、无 `/healthz`（go.mod 中仅有间接依赖） |
| 管理面认证 | 🔴 | 管理 API 完全信任上游反向代理 |
| 多租户 | 🔴 | Key 是扁平命名空间；无租户/项目维度 |
| Web 控制台 | 🔴 | 没有任何 UI |
| 原生提供方协议（Anthropic/Gemini/Bedrock） | 🔴 | 所有提供方一律按 `openai_compatible` 处理 |
| 余额账户 / 支付 / 发票 | 🔴 | 成本计算之外一无所有 |
| 测试 / CI / 部署产物 | 🔴 | 零个 `*_test.go`；无 CI 配置；有 Dockerfile 但无 compose/Helm |
| 提供方 API Key 保护 | 🔴 | `AIProvider.APIKey` 以**明文 varchar** 存储，而虚拟 Key 有 AES 加密——既不一致，也是安全缺口 |

模式很清楚：**数据面的治理内核（Key、配额、审计）确实扎实；而让它具备韧性（路由）、可售卖（计费）、可运维（可观测性）、可被采用（工程质量）的一切都缺失。**

## 按能力域的差距

七个能力域，大致按阻塞采用的先后排序。每条差距标注*谁需要*（[愿景](01-product-vision.md)中的画像：平台团队 / 转售商 / SaaS 团队）以及*为什么大多数部署都会撞上*。

### 域 1 · 流量路由与韧性 → [D01](design/01-routing-and-lb.md)

| 差距 | 谁 | 为什么重要 |
| --- | --- | --- |
| 加权 / 延迟感知 / 成本感知的 LB 策略 | 全部 | 随机选择浪费了现有 `Weight` 字段，无法表达"90% 走便宜的提供方、10% 做金丝雀" |
| 带重试策略的自动故障转移 | 全部 | 单一提供方故障是每周都会发生的事件；没有故障转移，网关*放大*故障面而不是收敛它 |
| 熔断 + 被动健康检查 | 全部 | 向已死的提供方重试给每个用户加延迟；熔断器在毫秒级卸载流量 |
| 降级链（提供方 → 提供方 → 降级模型） | 平台、SaaS | "gpt-4o → claude-sonnet → 本地 llama"是韧性的经典模式 |
| 会话亲和与熔断的交互 | SaaS | 被钉住的提供方熔断时，亲和必须让路，否则会话硬失败 |

这是优先级最高的差距：一个扛不住提供方故障的网关，是客户花钱加进来的单点故障。

### 域 2 · 协议适配 → [D02](design/02-protocol-adapters.md)

| 差距 | 谁 | 为什么重要 |
| --- | --- | --- |
| 出口：原生调用 Anthropic / Gemini / Bedrock / Azure | 全部 | 最好的模型并不都在 OpenAI 兼容端点后面；没有转换，"提供方自由选择"是虚构的 |
| 入口：Anthropic Messages / Responses API 入口 | SaaS、平台 | 基于 Claude SDK 或新 Responses API 构建的客户端不应被迫重写 |
| 用量归一化（缓存 Token、推理 Token） | 全部 | 计费与审计需要一个规范用量模型，与上游方言无关 |

### 域 3 · 商业化 → [D03](design/03-billing-and-monetization.md)

| 差距 | 谁 | 为什么重要 |
| --- | --- | --- |
| 余额账户（预付/后付）与原子扣减 | 转售商、平台 | 配额限制*速率*；只有余额限制*花费*。分摊与转售都需要账户 |
| 交易流水账（充值/扣减/冻结/退款，幂等） | 转售商 | 没有复式记账出处的资金变动无法审计、无法答客户问 |
| 多币种 + 阶梯/分组定价 | 转售商 | CNY 硬编码（`credits.go`）阻塞所有非 CNY 部署；转售利润要求售价与成本解耦 |
| 欠费停用 + 宽限期 | 转售商 | 让预付费真正生效的执行环节 |
| 预算告警 | 平台 | 财务想在 80% 时收到预警，而不是在 100% 时收到惊吓 |
| 订阅套餐、支付网关、发票 | 转售商 | 完整售卖闭环：这是把网关变成生意平台的部分 |

### 域 4 · 多租户与访问控制 → [D04](design/04-multi-tenancy-and-auth.md)

| 差距 | 谁 | 为什么重要 |
| --- | --- | --- |
| 管理面认证 | 全部 | "假设有反向代理"作为开源产品不可发布；第一次 `docker run` 就把管理 API 暴露给了局域网 |
| 租户 → 项目 → Key 层级 | 平台、转售商 | 成本归集、配额继承、数据隔离都挂在这棵树上 |
| RBAC（owner/admin/member/viewer） | 平台、转售商 | 看审计日志的人不应该是能明文取回 Key 的人 |
| OIDC / SSO | 平台 | 任何带登录页的企业软件的入场券 |

### 域 5 · 可观测性 → [D05](design/05-observability.md)

| 差距 | 谁 | 为什么重要 |
| --- | --- | --- |
| Prometheus 指标（`/metrics`） | 全部 | 没有请求/延迟/Token/错误序列，运维就是盲飞；这也是延迟感知路由的数据基础 |
| `/healthz` `/readyz` | 全部 | Kubernetes 和所有负载均衡器都需要 |
| OpenTelemetry 追踪 | 平台 | "这个请求为什么慢"需要贯穿 路由→上游→审计 的 span |
| 仓库自带 Grafana 看板 | 全部 | 需要自己组装的可观测性不会被使用 |

### 域 6 · 安全与合规 → [D06](design/06-security-and-guardrails.md)

| 差距 | 谁 | 为什么重要 |
| --- | --- | --- |
| 填上现有桩代码后面的 PII 引擎 | 平台 | `pii.go` 的 block/redact/log 框架接好了，却什么都检测不到；合规叙事需要它是真的 |
| 护栏管线（提示注入、内容安全） | 平台、SaaS | 网关是全组织 AI 安全策略天然的收口点 |
| 提供方 API Key 加密 | 全部 | 上游 Key 今天在 MySQL 里是明文；必须获得虚拟 Key 已有的 AES 待遇 |
| 审计体加密选项 | 平台 | 提示词请求体是网关存储的最敏感数据 |

### 域 7 · 工程与生态 → [D10](design/10-deployment-and-ops.md)

| 差距 | 谁 | 为什么重要 |
| --- | --- | --- |
| 测试 + CI | 全部 | 零测试意味着每次贡献都是赌博；没有 CI 的开源换不来信任 |
| docker-compose / Helm | 全部 | "首个请求耗时"是基础设施项目的漏斗顶端指标 |
| PostgreSQL / SQLite 支持 | 全部 | 只支持 MySQL 砍掉一半受众；SQLite 让演示变得零门槛 |
| 英文文档、CONTRIBUTING、OpenAPI 规范 | 全部 | 一个仓库和一个项目的区别 |
| Web 控制台 → [D08](design/08-web-console.md) | 全部 | 采用的现实：评估者先看控制台，再读 API 文档 |
| 扩展 Hook / MCP 网关 → [D09](design/09-extensibility.md) | 未来 | 把小众需求挡在内核之外的泄压阀 |

## 把差距读成战略

排序遵循依赖与采用逻辑，而不是工作量：

1. **先韧性与可运维（P0）** —— 路由/故障转移、指标、管理认证、部署产物、测试。它们让网关*敢跑在生产上*；做不到这一点，其他都不重要。
2. **再商业闭环（P1）** —— 租户、余额、控制台 MVP。它们让网关对缺少选项的转售商画像*值得跑*，并为平台团队解锁分摊。
3. **然后差异化（P2）** —— 协议适配、护栏、语义缓存、支付。它们赢下对比评测。
4. **持续面向未来（P3）** —— 插件、MCP、事件总线。它们在生态生长的同时保持内核小。

带出口标准的分阶段计划见[路线图](03-roadmap.md)。
