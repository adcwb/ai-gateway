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
- MCP：脚本化 Agent 会话（一次模型调用 + 两次工具调用）在审计里体现为一个带工具条目的会话；验证禁用工具被拒绝、参数护栏拦截生效。

## 实现说明（ADR 补记）

本轮交付的只是 **MCP 网关**这一块（代理 + 工具治理）——通用 Hook 分发器（`internal/biz/extension/`）、事件总线（`internal/biz/eventbus/`）、`ai_extensions`/`ai_event_cursors` 这一轮明确排除在外，仍是 🔴。

- **包拆分**：`internal/biz/mcpgw/`（新增）承载 JSON-RPC 2.0 消息形状，以及一个把单条消息转发给上游 Streamable HTTP MCP 服务器的 `Client`——对 `biz` 无依赖，和 `guardrail`/`vectorindex` 同样的拆分方式。治理逻辑（白名单、护栏链、配额、审计）作为消费方留在 `biz`（`mcp_admin.go` 做 CRUD，`mcp_proxy.go` 是处理器）。
- **传输覆盖范围**：只代理单条（非批量）**POST** JSON-RPC 消息。GET（该传输可选的服务端发起 SSE 推流）返回 405；DELETE（会话终止）返回 204、没有服务端状态需要清理——这是一个无状态代理，会话只是一个透传给/来自上游服务器的不透明 `Mcp-Session-Id`。批量（`[]`）JSON-RPC 请求被 `mcpgw.ParseRequest` 拒绝（按单对象反序列化，遇到数组会失败)——真实 MCP 客户端绝大多数每次 POST 只发一条消息，为批量做逐消息治理/审计扇出被判定不值得引入的复杂度。
- **凭证与鉴权模型**：Agent 用和模型流量一样的 `sk-vk-*` 虚拟 Key 鉴权，走的是完全相同的 `middleware.VirtualKeyAuth.ProxyMiddleware`——"模型和工具用同一套凭证体系"是字面意义上的实现（同一个中间件实例、同一次顶层请求计数配额预留），而不只是凭证格式上的一致。`ai_mcp_servers`（新表）注册上游服务器的方式与 `ai_providers` 注册模型提供方一致：全局对象、仅平台管理员可变更、可选的 bearer 凭证用同一套 `pkg/aes.go` 加密静态存储。
- **工具治理**：`AIVirtualKey.ToolWhitelist`（新增加法式 JSON 列）精确复刻 `AllowedModels` 的语义——为空/缺省 = 上游暴露的所有工具都允许。不在白名单内的工具在 `tools/call` 上被拒绝（JSON-RPC 错误 `-32001`，不会调用上游）**并且**从 `tools/list` 响应里过滤掉，Agent 甚至看不到它调不了的工具。
- **参数/结果护栏扫描原样复用 D06 的链路**——和模型 prompt/响应走的同一条 `resolvePIIPolicy` → `buildChainForPolicy` → `guardrail.Chain.Run` 路径，只是扫描对象换成工具调用的 `arguments` JSON（入向)和 `CallToolResult.content` 里某个文本块（出向）。这只对配置了 `checker_chain`（可插拔路径）的策略生效——仍停留在旧单引擎路径（`RuleConfig`+`Action`，无链）的策略不会被 MCP 流量咨询，因为 `mcpGuardrailScan` 直接调用 `buildChainForPolicy`，不会回退到 `scanPII`。被拦截的参数不会到达上游服务器（JSON-RPC 错误 `-32002`）；被拦截的结果会替换成一个 `[blocked]` 内容块并带 `isError: true`，而不是转发工具的真实输出。改写只处理常见的单文本块结果形状——多文本块的 `content` 数组不做改写（发现记录仍会保留），因为改写后的文本无法无歧义地映回多个原始块。
- **配额**：工具调用消耗 Key 现有的顶层请求计数配额（`VirtualKeyAuth.ProxyMiddleware` 对它包裹的每条路由本来就会做的同一次 `CheckAndReserve` 预留)——设计第 2 点要求的独立 `QuotaDimToolCall`（自己的 Redis Lua 脚本桶）**没有**实现。这是一个真实的范围缩减：工具调用流量和模型调用流量目前共享同一个 Key 级配额计数器，不是独立预算。
- **审计复用现有 `ai_gateway_audit_logs` 表**，而不是另建一张 MCP 专用表：`protocol="mcp"`，`model` 列被复用来承载 `"<serverName>"`（非工具调用方法）或 `"<serverName>/<toolName>"`（`tools/call`）——今天在控制台既有的审计页面里无需任何新 UI 即可看到。`resolveGatewaySessionID`（未改动）依然生效，所以工具调用会和模型调用归入同一套会话启发式，只是没有构建设计第 4 点要求的那种专门的"Agent 会话"概念（只是复用现有的）。

### Batch + Files API 代理（本轮新增）

上文"未来协议姿态"一节只给了这块一句话的配方（"async-job passthrough with job-ID mapping and deferred usage settlement on batch completion"，即异步任务透传 + job-ID 映射 + 批次完成后的延迟计费结算），没有数据模型——和 MCP 那一块不同，这是本轮从零设计的，不是照抄已有规格。

- **范围**：只代理 `openai_compatible` 方言的 provider。Anthropic 单独的 Message Batches API 不做跨方言翻译——留给以后按同一配方追加，不是重新设计。
- **Provider 选择**：Files/Batches 请求不带 `model` 字段，没法像模型请求那样靠 model-mapping 选 provider。`POST /ai/v1/files` 与 `POST /ai/v1/batches` 要求带 `X-AIGW-Provider` 请求头（provider 名称）；后续按 id 的 `GET`/`DELETE` 调用从本地影子行解析 provider，而不用重复传请求头，因为文件/批次一旦创建就是 provider 域内资源。
- **数据模型（新增，加法式）**：`ai_proxy_files`（`AIProxyFile`）与 `ai_batch_jobs`（`AIBatchJob`）——只做影子记账，不存文件字节。`AIBatchJob.SettledAt` 同时充当结算轮询器的待处理队列判定条件（`WHERE settled_at IS NULL`）。
- **延迟结算**：`GatewayUseCase.StartBatchSettlementPoller`（60 秒轮询一次，和 D01 主动健康探测同样的写法）轮询未到终态的批次状态；一旦变成 `completed`，拉取一次输出文件，把 JSONL 每一行结果的用量按模型累加（一个批次的各行理论上可以指向不同模型），再通过 `BillingManager.Settle` 按 OpenAI 公开的 50% 批次折扣、逐模型结算一笔汇总账单。由于提交时没有走 `Admit`/预冻结（批次真正跑之前根本不知道会消耗多少 token），结算这里构造了一个零预估额的 `FreezeHandle`，靠 `Settle` 的"预估 vs 实际"差值逻辑做一次纯粹的扣费。
- **明确排除的范围**：批次处于 `in_progress` 时的增量结算、按结果每一行单独出审计记录（现在是一个批次一条汇总记录）、输出文件拉取失败超出下一轮轮询周期的重试（保持未结算、下一轮重试——遵循项目"经济上 fail-open"的既有原则）。Batch/Files 的控制台 UI（provider 选择、任务状态）仍是仅 API 可用。
- MCP：脚本化 Agent 会话（1 次模型调用 + 2 次工具调用）在审计中呈现为一个会话且含工具条目；不允许的工具被拒绝；参数护栏阻断得到验证。
