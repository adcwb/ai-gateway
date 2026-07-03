# D02 · 协议适配

> [English version](../../design/02-protocol-adapters.md) · [ai-gateway 文档套件](../README.md)的一部分

| | |
| --- | --- |
| **阶段** | P2（Batch/Files API 为 P3） |
| **依赖** | [D01 路由](01-routing-and-lb.md)（路由选定提供方，适配器讲它的方言） |
| **被依赖** | [D03 计费](03-billing-and-monetization.md)与审计（消费归一化用量）、[D07 缓存](07-caching-strategies.md) |

## 背景

今天所有提供方都被假定为 OpenAI 兼容：`AIProvider.ProviderType` 默认 `openai_compatible`，除路径重写外没有任何分支（`rewriteOpenAIPathForProvider()` 处理 DashScope 的 rerank 路径）。这意味着：

- Anthropic、Gemini、Bedrock 的模型只能通过第三方兼容层间接接入——提供方选择被方言而非质量与价格约束。
- 基于 Anthropic SDK 构建的客户端（越来越常见：Claude Code、MCP 工具链）完全无法使用网关。
- 用量核算默默假设 OpenAI 的 `usage` 形状；其他方言的缓存 Token、推理 Token 字段会被丢弃，污染计费。

适配层在两个方向上消除方言约束，这是 P2 出口标准的前提：*Claude SDK 客户端与 OpenAI SDK 客户端调用同一虚拟 Key、落到同一 Gemini 提供方，用量正确*。

## 协议矩阵

两条独立的轴。网关的内部表示（IR）居中，工作量是 `N + M` 个翻译器，而不是 `N × M`。

```mermaid
flowchart LR
    subgraph Inbound["入口（客户端方言）"]
        I1["OpenAI Chat Completions<br/>/ai/v1/chat/completions"]
        I2["OpenAI Responses API<br/>/ai/v1/responses"]
        I3["Anthropic Messages<br/>/anthropic/v1/messages"]
    end
    IR["内部表示 IR<br/>（规范请求 + 用量）"]
    subgraph Outbound["出口（提供方方言）"]
        O1[openai_compatible]
        O2[anthropic]
        O3[gemini]
        O4[bedrock]
        O5[azure_openai]
    end
    I1 --> IR --> O1
    I2 --> IR --> O2
    IR --> O3
    I3 --> IR
    IR --> O4
    IR --> O5
```

按需求排序的落地顺序：出口 `anthropic` → `gemini` → `azure_openai` → `bedrock`；入口 `anthropic messages` → `responses`。`openai_compatible` 保持为恒等适配器与默认值，今天的行为分毫不差地保留。

### 快速路径保证

入口方言 == 出口方言时（OpenAI→openai_compatible，绝大多数流量），适配层**不得**经过 IR 往返。恒等适配器直接透传请求体，只做现有的定点变更（`replaceModelInBody()`、`injectStreamUsageOption()`、`injectPromptCacheKey()`、`injectModelExtraParams()`）。完整的解析/序列化只在真正需要转换时支付。这守住了热路径预算（设计原则 5）。

## 内部表示

不是所有 API 的无损并集，而是**面向路由与核算**的规范形态，外加透传扩展袋：

```go
// internal/biz/protocol/ir.go
type ChatRequest struct {
    Model       string
    Messages    []Message      // role、content parts（text/image/audio/tool_call/tool_result）
    System      string         // Anthropic 单列、OpenAI 内嵌 —— IR 单列
    Tools       []Tool
    Stream      bool
    MaxTokens   *int
    Temperature *float64
    // ... 其他一等公民的通用参数
    Extensions  map[string]json.RawMessage // 方言特有参数：出口方言认识则转发，否则丢弃并在审计中注记
}

type Usage struct {
    InputTokens        int
    OutputTokens       int
    CacheReadTokens    int // OpenAI: prompt_tokens_details.cached_tokens · Anthropic: cache_read_input_tokens
    CacheWriteTokens   int // Anthropic: cache_creation_input_tokens · 其他方言缺席
    ReasoningTokens    int // OpenAI: completion_tokens_details.reasoning_tokens · Gemini: thoughtsTokenCount
    Raw                json.RawMessage // 提供方原生 usage 对象，保留在审计中以备溯源
}
```

`Usage` 是 `writeAuditLog()`、`QuotaManager.CommitTokens()` 与 `calcCredits()` 消费的唯一形状（`internal/biz/credits.go` 已经单独为缓存读 Token 定价——IR 让非 OpenAI 方言也终于能喂上这个字段）。

## 适配器接口

```go
// internal/biz/protocol/adapter.go

// OutboundAdapter 讲一种提供方方言。由 AIProvider.ProviderType 选择。
type OutboundAdapter interface {
    // BuildRequest 将 IR 映射为提供方 HTTP 请求（BaseURL + 方言路径、认证头风格、请求体）。
    BuildRequest(ctx context.Context, p *model.AIProvider, req *ChatRequest) (*http.Request, error)
    // ParseResponse 将非流式提供方响应映射为 IR 响应 + 归一化 Usage。
    ParseResponse(resp *http.Response) (*ChatResponse, *Usage, error)
    // StreamDecoder 将提供方 SSE/分块流包装为 IR 事件流。
    StreamDecoder(body io.Reader) StreamDecoder
}

// InboundCodec 讲一种面向客户端的方言。由路由选择。
type InboundCodec interface {
    DecodeRequest(r *http.Request) (*ChatRequest, error)
    EncodeResponse(w http.ResponseWriter, resp *ChatResponse) error
    // StreamEncoder 以客户端期待的线格式写出 IR 流事件，
    // 包括方言正确的事件名、role 增量与终止符（[DONE] vs message_stop）。
    StreamEncoder(w http.ResponseWriter) StreamEncoder
}
```

注册是编译期的（经 Wire 注入的注册表 `map[string]OutboundAdapter`，不搞 `init()` 魔法），符合项目"无运行时魔法"的立场；社区适配器以"一个包 + 一行注册"的 PR 形式到来。`rewriteOpenAIPathForProvider()` 并入 `openai_compatible` 适配器的 `BuildRequest`，独立函数删除。

### 流式转换

困难的那 20%。设计规则：

1. **事件级状态，而非 Token 级。** 翻译器为每条流维护小型状态机（当前 tool-call 序号、content block 序号）——Anthropic 的 `content_block_start/delta/stop` 与 OpenAI 带下标的 `tool_calls` 增量互相映射。
2. **usage 到达时机各不相同**（OpenAI：配 `stream_options.include_usage` 的最终 chunk；Anthropic：`message_delta`；Gemini：chunk 上的 `usageMetadata`）。解码器无论来源如何都发出终结的 `UsageEvent`；审计/计费只消费它。
3. [D01](01-routing-and-lb.md) 的**首 chunk 承诺规则**原样适用：编码器写出第一个字节后，故障转移关闭。
4. 每 chunk 开销预算：p99 < 5 ms（P2 出口标准）——翻译器不得缓冲整个响应；它们以 chunk 进、事件出的方式工作。

### 认证与端点方言

| ProviderType | 认证 | 路径形态 | 备注 |
| --- | --- | --- | --- |
| `openai_compatible` | `Authorization: Bearer` | `/v1/chat/completions` | 今天的行为 |
| `anthropic` | `x-api-key` + `anthropic-version` | `/v1/messages` | 版本头按提供方可配 |
| `gemini` | `x-goog-api-key`（或 OAuth） | `/v1beta/models/{model}:generateContent` / `:streamGenerateContent` | 模型在*路径*里——URL 构造归 `BuildRequest` 所有 |
| `azure_openai` | `api-key` 头 | `/openai/deployments/{deployment}/chat/completions?api-version=…` | deployment 名 ≠ 模型名：存于 `AIModelItem` 扩展参数 |
| `bedrock` | SigV4 签名 | `/model/{modelId}/invoke(-with-response-stream)` | 提供方配置需 AWS 凭证；其流式线格式（event-stream）有独立解码器 |

提供方特有设置（anthropic-version、api-version、region、deployment 映射）放入 `ai_providers` 新增的可空 JSON 列 `adapter_config`。

## 入口

在 `internal/server/http.go` 注册新路由，由同一个 `virtual_key_auth` 中间件守卫：

- `POST /anthropic/v1/messages` —— Anthropic Messages 编解码器。除 Bearer 外也接受 `x-api-key: sk-vk-*`（Anthropic SDK 惯例）。
- `POST /ai/v1/responses` —— OpenAI Responses API 编解码器，映射到 IR（先做无状态子集；`previous_response_id` 链式调用在需求被证明前不做——记为开放问题）。

模型解析、配额、路由、审计、计费全部与方言无关，因为它们在解码后的 IR 上运行。

## 数据模型变更

| 表 | 变更 |
| --- | --- |
| `ai_providers` | `adapter_config json`（可空） |
| `ai_model_items` | Azure deployment 映射复用现有扩展参数（无 schema 变更） |
| `ai_gateway_audit_logs` | `inbound_protocol varchar(32)`、`cache_write_tokens int`、`reasoning_tokens int`（缓存读已在计费路径中） |

## 涉及代码

| 位置 | 变更 |
| --- | --- |
| `internal/biz/protocol/`（新包） | IR、适配器/编解码接口、注册表、各方言实现 |
| `internal/biz/gateway.go` `ProxyRequest` | 入口编解码 → IR 管线 → 出口适配器；保留快速路径 |
| `internal/biz/gateway.go` 请求体变更辅助函数 | 并入恒等适配器 |
| `internal/server/http.go` | 新入口路由 |
| `internal/biz/credits.go` `calcCredits` | 接受 `Usage`（补上缓存写定价，`AIModelItem.CacheWritePricePerMillion` 已有价格字段） |

## 测试与验证

- 各适配器的 golden-file 测试：录制的提供方 fixture（请求/响应/流转录）→ 断言精确的 IR 与精确的重编码输出。fixture 即契约；提供方 API 漂移体现为 fixture diff。
- 跨方言矩阵测试：每个入口编解码 × 每个出口适配器，跑一段规范会话（文本 + 工具调用 + 流式），断言归一化 `Usage` 相等。
- 快速路径压测：证明 OpenAI→openai_compatible 相对适配层引入前的基线零新增分配/延迟。
