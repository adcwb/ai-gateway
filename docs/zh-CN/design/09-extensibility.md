# D09 · 可扩展性与 Agent 时代

> [English version](../../design/09-extensibility.md) · [ai-gateway 文档套件](../README.md)的一部分

| | |
| --- | --- |
| **阶段** | P3（[D06](06-security-and-guardrails.md) 需要的 Hook 点可提前落地） |
| **依赖** | [D02 协议适配](02-protocol-adapters.md)（IR 是扩展的通货）、[D04](04-multi-tenancy-and-auth.md)（插件配置按租户作用域） |
| **被依赖** | 社区适配器、外部集成 |

## 背景

每个网关都会积累小众需求："日志接我们的 SIEM"、"自定义计价公式"、"调用昂贵模型前先走我们的审批服务"、"消费同步到 ERP"。把这些吸进内核会让内核不可维护；拒绝它们会让项目不可采用。答案是一个小而稳定的扩展面——它同时也是面向未来的机制：下一个协议时代到来时（Agent 工具调用、MCP），应该以*适配器和 Hook* 的形式落地，而不是重写。代码库里已有的先例指明了方向：护栏 checker（[D06](06-security-and-guardrails.md)）、协议适配器（[D02](02-protocol-adapters.md)）、支付网关（[D03](03-billing-and-monetization.md)）都是接口后面的编译期注册表——本文把这个模式一般化，并增加进程外选项。

## Hook 点

四个，刻意少——每个的输入/输出都是 IR 级类型，因此 Hook 与方言无关：

| Hook | 时机 | 可以 | 同步？ |
| --- | --- | --- | --- |
| `pre_request` | 认证+护栏之后、路由之前 | 改写请求 IR、拒绝（带原因）、注记（标签流入审计/计费） | 同步，截止时间约束 |
| `post_response` | 上游之后、客户端编码之前（非流式；流式只有终结事件） | 改写响应 IR、注记 | 同步，截止时间约束 |
| `on_audit` | 审计条目定稿后 | 消费（只读） | 异步 |
| `on_billing` | 流水条目提交后 | 消费（只读） | 异步 |

规则镜像护栏链：每 Hook 截止时间（同步默认 100 ms）、每扩展的失败开放/关闭配置、panic 收容、调用计入指标，且在改写或拒绝时以类似 `attempts` 轨迹的方式在审计中可见。

## 交付机制（ADR）

- **背景：** 第三方代码如何挂到这些 Hook 上？
- **选项：** (a) Go 编译期注册（import + 注册行，重新构建）；(b) HTTP webhook 进程外；(c) WASM 进程内沙箱（wazero）；(d) Go `plugin` 包（.so 加载）；(e) 内嵌脚本（Lua/JS）。
- **决策：** 交付 **(a) + (b)**；(c) 待真实需求再评估。(d) 直接否决——Go plugin 对工具链/版本脆弱且仅 Linux。(e) 否决——脚本运行时是项目不想要的支持面与安全面。
- **后果：**
  - **(a) 编译期**是*性能关键且受信*扩展的落地方式（新协议适配器、向量后端、支付网关已按此运作）。代价是重新构建。文档化的 `cmd/server/extensions.go` 给 fork 一个受祝福的接触点，让差异保持易维护。
  - **(b) webhook** 是*集成类*扩展零重构的落地方式：一个扩展 = URL + 订阅的 Hook + HMAC 密钥（经管理 API / 控制台设置配置）。同步 Hook POST IR 信封、读回 `{action: pass|mutate|reject, patch…}`；异步 Hook 批量投递。仅此已覆盖 SIEM 导出、审批流、ERP 同步——真实需求的大多数。
  - **(c) WASM** 能以进程外的安全获得进程内的速度，代价是要设计并冻结一套 ABI。等 webhook 延迟切实阻塞某类采用者时再回头看（记为开放问题，不是承诺）。

## 事件总线

`on_audit`/`on_billing`（加上熔断迁移与配额事件）一般化为单一内部事件流 + 可插拔 sink——扩展性的异步一半：

- **Sink：** webhook（批量、HMAC 签名、至少一次投递，带重试/退避——复用 `AuditWorker` 的 spill/重试机制）与 Kafka（可选构建；按事件类型分 topic）。投递游标存 Redis，sink 可断点续传。
- **信封：** `{event_type, event_id (ULID), occurred_at, tenant_id, payload}`，带 schema 版本字段（`v`），消费者能安然度过加法式变更。
- 由此无需改内核即可解锁的消费者：外部财税开票（[D03](03-billing-and-monetization.md) 划出范围的税务）、SIEM/合规归档、按量计费的 CRM 同步、自定义告警。

## MCP 网关

对 Agent 时代的下注。MCP（Model Context Protocol）正在成为 Agent 触达工具的标准方式；工具流量是平台团队接下来必然要治理的对象，方式与今天治理模型流量完全一致——同样的认证、同样的配额、同样的审计问题（"哪个 Agent 用什么参数调了哪个工具？"）。

P3 范围：

1. **MCP 代理：** 网关暴露虚拟 MCP server 端点（Streamable HTTP 传输）；每个映射到一个注册的上游 MCP server（`ai_mcp_servers`：名称、传输配置、认证）。客户端用同样的 `sk-vk-*` Key 认证——模型*与*工具共用一套凭证体系。
2. **工具调用治理：** 按 Key 的工具白名单（镜像模型白名单）、参数级护栏检查（[D06](06-security-and-guardrails.md) 的链跑在工具参数/结果上——注入经常*通过工具结果*到达）、配额维度扩展 `QuotaDimToolCall`。
3. **审计：** 工具调用作为一等条目进入审计中心（server、tool、参数摘要、结果摘要、延迟、Agent 会话）——满足 P3 出口标准：工具调用可见且受配额约束。
4. **Agent 会话：** 现有会话亲和标识（`resolveGatewaySessionID`）扩展为把一个 Agent 的模型调用*与*工具调用归入同一可审计会话——控制台的会话视图从此讲完整个 Agent 的故事。

明确不在范围内：编写/托管 MCP server（网关治理工具，不实现工具），以及 A2A 类 Agent 间协议——等它们稳定（适配器架构就是保险单）。

## 未来协议姿态

常设方针，让每个新 API 时代成为有界任务而非危机：新提供方方言 = 一个 `OutboundAdapter`；新的客户端表面 = 一个 `InboundCodec` + 路由；新治理维度 = 配额维度常量 + 审计列（两者都设计为加法式）。IR 只做加法生长；`Extensions` 袋吸收 IR 尚未建模的内容。Batch API 与 Files API 代理（路线图 P3-4）循此配方：异步作业透传 + 作业 ID 映射 + 批完成时的延迟用量结算。

## 数据模型与配置

| 表 | 用途 |
| --- | --- |
| `ai_extensions` | 注册的 webhook 扩展：name、url、hooks json、hmac 密钥（加密）、fail_mode、租户作用域、is_enabled |
| `ai_mcp_servers` | 上游 MCP 注册 |
| `ai_event_cursors` | 各 sink 的投递位置 |
| `ai_virtual_keys` | `tool_whitelist json`（加法式） |

## 涉及代码

| 位置 | 变更 |
| --- | --- |
| `internal/biz/extension/`（新增） | Hook 分发器、webhook 客户端、注册表 |
| `internal/biz/eventbus/`（新增） | 事件流 + sink |
| `internal/biz/mcp/`（新增） | MCP 代理、工具治理 |
| `internal/biz/gateway.go` | 四个 Hook 调用点 |
| `internal/server/http.go` | MCP 传输路由、扩展管理 API |

## 测试与验证

- Hook 语义：截止时间、失败模式、panic 收容、改写/拒绝往返（与护栏链共享表测试——同一契约）。
- webhook 扩展一致性套件：一个微型参考扩展（仓库 `examples/extensions/`），CI 对 compose 栈运行——兼作社区模板。
- 事件总线：sink 故障下的至少一次投递（流中杀掉 sink，断言从游标恢复且不丢）。
- MCP：脚本化 Agent 会话（1 次模型调用 + 2 次工具调用）在审计中呈现为一个会话且含工具条目；不允许的工具被拒绝；参数护栏阻断得到验证。
