# D07 · 缓存策略

> [English version](../../design/07-caching-strategies.md) · [ai-gateway 文档套件](../README.md)的一部分

| | |
| --- | --- |
| **阶段** | P2 |
| **依赖** | [D02 协议适配](02-protocol-adapters.md)（缓存键/回放在 IR 上工作）、[D03 计费](03-billing-and-monetization.md)（命中计价）、[D01 路由](01-routing-and-lb.md)（缓存位于路由之前） |
| **被依赖** | —— |

## 背景

现有三个类缓存机制，但没有一个缓存*响应*：

- Key 解析缓存（L1/L2，`internal/biz/key_cache.go`）——解析凭证。
- 会话亲和（`sticky_session.go`）+ `injectPromptCacheKey()` —— 让*提供方侧*的提示词缓存更常命中；提供方照样收费（折扣的缓存读 Token，`credits.go` 已定价）。
- 提供方自己的提示词缓存——是上游的功能，不是我们的。

缺的是彻底消除上游调用的那一层：相同或语义等价的请求由网关直接应答。对 SaaS 画像（FAQ 型流量、重试风暴、模板化提示词）这是直接的单位经济杠杆；它与上面的亲和机制是*叠加*而非替代关系（语义*未命中*的请求仍受益于提示词缓存友好的粘性路由）。

在管线中的位置：护栏之后（[D06](06-security-and-guardrails.md)——被阻断的请求绝不能被缓存应答）、路由之前（命中即跳过路由、Token 配额提交与计费结算，改按命中策略计费）。

## 两种缓存，一个接口

| | 精确缓存 | 语义缓存 |
| --- | --- | --- |
| 键 | *归一化* IR 的 SHA-256 | 归一化提示词文本的 embedding |
| 匹配 | 相等 | 余弦相似度 ≥ 阈值（默认 0.95，按 Key 可调） |
| 后端 | Redis（现有依赖） | 可插拔向量索引 |
| 默认 TTL | 5 分钟 | 1 小时 |
| 风险 | 无（相同输入 ⇒ 相同答案可接受） | 答错风险：以阈值 + 主动开启缓解 |
| 默认状态 | 按 Key 关闭，一个开关开启 | 按 Key 关闭，显式开启并配阈值 |

**两种键计算前先归一化**（在 [D02](02-protocol-adapters.md) IR 上做，与方言无关）：丢弃非语义字段（`stream`、`user`、请求 ID），JSON 键序规范化，解析*虚拟*模型名（映射前——两个 Key 映射到同一后端时，只有各自解析出的模型不同才分开缓存）。作用域前缀：`tenant:resolved_model:params_digest`，其中 `params_digest` 覆盖生成参数（temperature、max_tokens、tools）——temperature 0.2 的答案不得服务 temperature 1.0 的请求。

可缓存性闸门（最先检查）：无工具调用在途、无多模态部分（v1）、`n=1`、请求小于尺寸上限、方法为 chat/completions 或 embeddings（embeddings 是*最好*的缓存客户：确定性、精确匹配友好）。

### 向量后端（ADR）

- **背景：** 语义缓存需要 ANN 检索；项目的依赖姿态是"MySQL + Redis 即完整部署"。
- **选项：** (a) Redis 向量能力（Redis ≥ 8 / Redis Stack）；(b) 内嵌索引（进程内 HNSW，落盘持久化）；(c) 外部向量库（Milvus/Qdrant）。
- **决策：** 定义 `VectorIndex` 接口，(a) 为默认实现——它搭现有 Redis 依赖的便车（版本门控：连接的 Redis 不支持向量时，功能禁用并清晰打日志）——(c) 作为 P3 的社区适配器面。否决 (b)：各实例的索引会彼此漂移，破坏无状态原则。
- **后果：** 语义缓存要求较新的 Redis；旧 Redis 部署保留精确缓存。embedding **经网关自身**生成，调用运营者指定的 embedding 提供方/Key（与 [D06](06-security-and-guardrails.md) 的 LLM 裁判同一自举模式）：全程审计、按成本计费、一个配置项（`cache.embedding_model`）。

## 命中路径与流式回放

存储值：IR 级的最终响应（不是提供方线上字节），加原始用量与出处（provider、model、created_at、审计引用）。命中时：

1. 经入口编解码器响应——Anthropic 方言客户端可以命中 OpenAI 方言客户端创建的条目；存 IR（而非线格式）正是这件事成立的原因。
2. 客户端请求 `stream=true` 时，编码器**把缓存的补全回放为合成流**（按近似 Token 边界分块，零人工延迟）——为流式而写的客户端不能因为缓存应答而坏掉。
3. 审计条目照常写入，标记 `cache_hit=exact|semantic` 并引用来源条目——溯源原则 7 同样适用于缓存答案。
4. 响应头 `X-AIGW-Cache: hit-exact|hit-semantic|miss`（可关闭），供客户端观测。

故障收容：缓存查询出错（Redis 挂、索引超 20 ms 预算）⇒ 静默按未命中处理，请求正常继续。缓存永远不能让流量变得更糟。

## 缓存感知计费

按 Key 策略，在结算处执行（[D03](03-billing-and-monetization.md)）：

| 策略 | 命中收费 | 场景 |
| --- | --- | --- |
| `free`（默认） | 0 | 内部平台：缓存节省透传 |
| `discount` | 原始用量售价的可配百分比 | 转售商：对基础设施价值收毛利 |
| `full` | 售价的 100% | 最大化毛利的转售商；上游成本仍为 0 |

配额交互：命中消耗**请求数与并发**配额（它们是真实请求），默认不消耗 **Token** 配额（没有上游 Token 流动）——想要 Token 配额对齐的转售商有按 Key 的开关。指标：`aigw_cache_requests_total{cache_type,outcome}`（[D05](05-observability.md)）；经济看板展示"避免的上游成本" = 命中时原始用量成本之和。

## 失效与控制

- TTL 是主要机制（默认偏短；正确性优先于命中率）。
- 手动：管理 API `DELETE /ai/gateway/cache?scope=key|model|tenant`（按作用域前缀清空）——模型/策略变更后的运维逃生口。
- 自动：模型映射变更与护栏策略变更经现有失效 pub/sub 通道模式发布，清空受影响作用域。
- 单请求绕过：请求头 `Cache-Control: no-cache`（仅在该 Key 开启缓存时生效；记入审计）。

## 数据模型与配置

不加新 MySQL 表（缓存状态是 Redis 原生的，遵循无状态原则）。`ai_virtual_keys` 加法式新列 `cache_config json`（精确开关 + ttl、语义开关 + 阈值 + ttl、计费策略、Token 配额标志）。Redis 键遵循约定：`ai:gw:cache:x:{scope_digest}`（精确条目）、`ai:gw:cache:v:{tenant}`（向量索引名）、`ai:gw:cache:stats:{keyID}`（供控制台的命中计数）。

## 涉及代码

| 位置 | 变更 |
| --- | --- |
| `internal/biz/respcache/`（新增） | 归一化、精确存储、`VectorIndex` 接口 + Redis 实现、回放编码器胶水 |
| `internal/biz/gateway.go` `ProxyRequest` | 护栏后/路由前查询；结算成功后写入 |
| `internal/biz/billing.go` | 命中策略的结算路径 |
| `internal/service/gateway.go` + `internal/server/http.go` | 缓存清空端点 |
| `configs/config.yaml` / `conf.go` | `cache` 块（embedding 模型、全局上限） |

## 测试与验证

- 归一化：字段序/空白变体发生碰撞；任何生成参数变化不碰撞。工具调用与多模态请求绕过。
- 跨方言：经 OpenAI 编解码器创建的条目以字节正确的方式服务 Anthropic 编解码器的请求，同步与合成流两种模式均验证。
- 语义：在带标注的同义改写语料上做阈值扫描，记录精度/命中率权衡；默认阈值在语料上精度必须 ≥ 99%。
- 故障：负载中停掉 Redis ⇒ 零请求失败、全部未命中。
- P2 出口标准（[路线图](../03-roadmap.md)）：重复负载基准命中率 ≥ 30%，计费符合策略。
