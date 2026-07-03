# 03 · 路线图

> [English version](../03-roadmap.md) · [ai-gateway 文档套件](README.md)的一部分

四个阶段，每个阶段有主题、范围和**出口标准**——全部满足才算阶段完成的可观察条件。阶段是能力里程碑而非日历承诺；开发可以交叠，发布按序。

```mermaid
flowchart LR
    P0["P0 · 开源就绪<br/>有韧性、可运维"] --> P1["P1 · 商业闭环<br/>租户、余额、控制台"]
    P1 --> P2["P2 · 差异化<br/>协议、护栏、支付"]
    P2 --> P3["P3 · 面向未来<br/>插件、MCP、生态"]
```

## P0 · 开源就绪 —— 发布门槛

**主题：** 一个陌生人可以把它跑在生产上并信任它。P0 每一项落地之前，项目不进入"正式发布"状态。

| # | 交付物 | 设计文档 |
| --- | --- | --- |
| P0-1 | 加权负载均衡（启用 `AIProvider.Weight`）、重试 + 故障转移、基于被动健康检查的熔断 | [D01](design/01-routing-and-lb.md) |
| P0-2 | Prometheus `/metrics`、`/healthz`、`/readyz`；Grafana 看板 JSON 入仓 | [D05](design/05-observability.md) |
| P0-3 | 管理 API 认证：静态 admin token，随后 admin API key | [D04](design/04-multi-tenancy-and-auth.md) |
| P0-4 | 提供方 API Key 静态加密（堵上明文缺口） | [D06](design/06-security-and-guardrails.md) |
| P0-5 | `docker-compose up` 一条命令起全栈（网关 + MySQL + Redis） | [D10](design/10-deployment-and-ops.md) |
| P0-6 | 在 MySQL 之外支持 PostgreSQL | [D10](design/10-deployment-and-ops.md) |
| P0-7 | `internal/biz` 单元测试（配额、路由、计价、Key 缓存）+ 集成测试；GitHub Actions CI（test/lint/build/docker） | [D10](design/10-deployment-and-ops.md) |
| P0-8 | 英文 README、CONTRIBUTING、SECURITY.md、issue/PR 模板、管理 API 的 OpenAPI 规范 | [D10](design/10-deployment-and-ops.md) |

**出口标准**

- [ ] 配置两个提供方时杀掉其中一个，除在途请求外零用户可见错误；流量在熔断窗口内切走，恢复后切回。
- [ ] 干净机器上仅按 README 操作，`docker compose up` 到第一次成功的 `/ai/v1/chat/completions` 不超过 10 分钟。
- [ ] `curl /metrics` 暴露请求、延迟、Token、熔断序列；随附的 Grafana 看板能渲染它们。
- [ ] 没有任何管理端点在无凭证时响应。
- [ ] 每个 PR 上 CI 全绿：`go test ./...`（`internal/biz` 覆盖率 ≥ 60%）、`golangci-lint`、多架构 docker 构建。
- [ ] 完整测试套件在 MySQL 与 PostgreSQL 上都通过。

## P1 · 商业闭环 —— 值得付费

**主题：** 转售商画像可以运营预付费 API 业务；平台画像获得分摊。控制台让两者可见。

| # | 交付物 | 设计文档 |
| --- | --- | --- |
| P1-1 | 租户 → 项目 → Key 层级；行级隔离；配额继承 | [D04](design/04-multi-tenancy-and-auth.md) |
| P1-2 | 管理面用户账户 + RBAC（owner/admin/member/viewer） | [D04](design/04-multi-tenancy-and-auth.md) |
| P1-3 | 余额账户（预付/后付）、复式流水账、代理路径上的冻结→结算扣减、宽限期停用 | [D03](design/03-billing-and-monetization.md) |
| P1-4 | 多币种定价；与上游成本解耦的价格表；预算告警 | [D03](design/03-billing-and-monetization.md) |
| P1-5 | 成本归集报表（按租户/项目/Key/模型/标签），带按日预聚合 | [D03](design/03-billing-and-monetization.md) |
| P1-6 | 在现有 `pii.go` 桩后面落地内置规则 PII 引擎（block/redact/log） | [D06](design/06-security-and-guardrails.md) |
| P1-7 | Web 控制台 MVP：仪表盘、Key、提供方、审计、用量/计费视图，经 `embed.FS` 内嵌 | [D08](design/08-web-console.md) |

**出口标准**

- [ ] 转售商端到端流程在控制台内跑通：建租户 → 充值 → 发 Key → 消费 → 看余额下降 → 归零 → 请求被计费错误拒绝 → 再充值 → 流量恢复。
- [ ] 每条流水都能追溯到审计日志记录；流水之和恒等于账户余额（用测试固化该不变量）。
- [ ] member 角色能看用量，但不能明文取回 Key、不能改配额。
- [ ] 预算告警在配置阈值处触发（webhook/邮件）。
- [ ] 控制台做不出任何公开文档 API 做不到的操作。

## P2 · 差异化 —— 赢下对比

**主题：** 让 ai-gateway 成为*更好*的选择，而不只是可用的选择。

| # | 交付物 | 设计文档 |
| --- | --- | --- |
| P2-1 | 出口协议适配器：Anthropic、Gemini、Bedrock、Azure OpenAI（含流式） | [D02](design/02-protocol-adapters.md) |
| P2-2 | 入口 Anthropic Messages API + OpenAI Responses API | [D02](design/02-protocol-adapters.md) |
| P2-3 | 供审计 + 计费消费的用量归一化层（缓存/推理 Token） | [D02](design/02-protocol-adapters.md) |
| P2-4 | 精确响应缓存 + 语义缓存（可插拔向量后端），缓存感知计费 | [D07](design/07-caching-strategies.md) |
| P2-5 | 护栏管线：提示注入检测、话题围栏、输出安全；外部 PII 引擎（gRPC）对接 | [D06](design/06-security-and-guardrails.md) |
| P2-6 | OpenTelemetry 追踪，W3C 上下文透传 | [D05](design/05-observability.md) |
| P2-7 | 订阅套餐、支付网关抽象（Stripe / 支付宝 / 微信支付）、发票记录 | [D03](design/03-billing-and-monetization.md) |
| P2-8 | 控制台二期：计费中心、租户管理、模型/定价管理、系统设置 | [D08](design/08-web-console.md) |
| P2-9 | 控制台 OIDC/SSO | [D04](design/04-multi-tenancy-and-auth.md) |

**出口标准**

- [ ] 一个 Claude SDK 客户端和一个 OpenAI SDK 客户端调用同一个虚拟 Key、落到同一个 Gemini 提供方，审计与计费中的用量归一化正确。
- [ ] 流式转换在 p99 下每 chunk 增加 < 5 ms。
- [ ] 语义缓存在重复负载基准上命中率 ≥ 30%，命中按配置策略计费。
- [ ] 一笔 Stripe（或支付宝）充值走完 webhook→流水→余额 闭环，验签通过、重放幂等。
- [ ] 单个请求的 trace 呈现完整 span：认证 → 路由决策 → 上游调用 → 结算。

## P3 · 面向未来 —— 生态之赌

**主题：** 在表面积增长的同时保持内核小；为 Agent 时代做好准备。

| # | 交付物 | 设计文档 |
| --- | --- | --- |
| P3-1 | 扩展 Hook 点（pre-request / post-response / on-audit / on-billing）；webhook + 编译期插件注册；WASM 评估 | [D09](design/09-extensibility.md) |
| P3-2 | 事件总线：审计/配额/计费事件投递到 webhook 或 Kafka | [D09](design/09-extensibility.md) |
| P3-3 | MCP 网关：MCP server 代理、工具调用审计与治理、Agent 会话级配额 | [D09](design/09-extensibility.md) |
| P3-4 | Batch API 与 Files API 代理 | [D02](design/02-protocol-adapters.md) |
| P3-5 | Helm chart + Kubernetes 支持；SQLite 单文件演示模式 | [D10](design/10-deployment-and-ops.md) |

**出口标准**

- [ ] 一个社区编写的提供方适配器和一个护栏插件均在不改内核包的情况下发布。
- [ ] 流经 MCP 网关的工具调用带参数与结果出现在审计中心，且受配额约束。
- [ ] `helm install` 得到一套 3 副本高可用部署，并通过 P0 的故障转移测试。

## 跨阶段不变量

适用于所有阶段、优先级高于功能进度的规则：

1. **`/ai/v1/*` 永不破坏性变更。** 从 P0 起 OpenAI 兼容是公开契约。
2. **迁移只做加法。** 存量部署仅靠 `autoMigrate` 即可升级；破坏性 schema 变更必须先做版本化迁移工具决策（见 [D10](design/10-deployment-and-ops.md)）。
3. **热路径预算。** 任何给代理路径 p99 增加 > 2 ms 的功能必须异步化或可选。
4. **文档随代码交付。** 没有设计文档更新和用户文档的功能不算完成。
5. **中英对等。** 面向用户的文档与控制台文案在同一版本内双语落地。
