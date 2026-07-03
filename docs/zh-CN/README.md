# ai-gateway 文档

> English documentation: [docs/](../README.md) · 英文版为权威版本

**ai-gateway** 是一个使用 Go 编写的自托管、OpenAI 兼容的 AI 流量控制平面。它位于应用与各大模型提供方之间，以单一二进制提供虚拟 Key 管理、多维配额、审计日志、Token 统计、计费与多供应商负载均衡等能力。

本目录是指导项目从当前状态演进为完整开源 AI 网关的产品规划与技术设计套件。

## 阅读指引

请先按顺序阅读三篇顶层文档，它们回答"产品是什么、现在在哪、要去哪里"。[design/](design/) 下的设计文档是各能力域自成一体的深入设计，按需阅读。

### 顶层文档

| 文档 | 内容 |
| ---- | ---- |
| [01 · 产品愿景](01-product-vision.md) | 定位、目标用户、竞品格局、设计原则 |
| [02 · 差距分析](02-gap-analysis.md) | 基于真实代码库的能力现状盘点与差距分析 |
| [03 · 路线图](03-roadmap.md) | 分阶段交付计划（P0–P3），每阶段含出口标准 |

### 设计文档

每篇设计文档均标注所属路线图阶段及其与其他设计的依赖关系。

| 文档 | 阶段 | 内容 |
| ---- | ---- | ---- |
| [D01 · 路由与负载均衡](design/01-routing-and-lb.md) | P0 | 路由策略、加权负载均衡、故障转移、熔断、健康检查 |
| [D02 · 协议适配](design/02-protocol-adapters.md) | P2 | 双向协议转换（OpenAI / Anthropic / Gemini / Bedrock / Azure）、用量归一化 |
| [D03 · 计费与商业化](design/03-billing-and-monetization.md) | P1–P2 | 计价、余额账户、流水账、订阅套餐、支付网关、发票 |
| [D04 · 多租户与认证](design/04-multi-tenancy-and-auth.md) | P0–P2 | 租户/项目层级、管理面认证、RBAC、SSO |
| [D05 · 可观测性](design/05-observability.md) | P0–P2 | Prometheus 指标、OpenTelemetry 追踪、健康端点、看板 |
| [D06 · 安全与护栏](design/06-security-and-guardrails.md) | P1–P2 | PII 检测引擎、护栏管线、密钥安全 |
| [D07 · 缓存策略](design/07-caching-strategies.md) | P2 | 精确缓存、语义缓存、缓存感知计费 |
| [D08 · Web 控制台](design/08-web-console.md) | P1–P2 | 管理控制台：信息架构、逐页设计、技术选型 |
| [D09 · 可扩展性](design/09-extensibility.md) | P3 | 插件机制、Hook 扩展点、MCP 网关、事件总线 |
| [D10 · 部署与运维](design/10-deployment-and-ops.md) | P0 | 部署形态、多数据库支持、高可用、开源工程化 |

## 文档约定

- **英文版为权威版本。** 本中文镜像与英文版保持结构对齐；两者不一致时以英文版为准。
- **图统一使用 Mermaid。** 所有架构图与流程图使用 ` ```mermaid ` 代码块，GitHub 原生渲染。
- **设计引用真实代码。** 凡是提出对现有行为的改动，均引用实际文件与函数（如 `internal/biz/gateway.go`），撰写时已对照仓库核实。
- **重要决策采用 ADR 风格。** 按*背景 → 选项 → 决策 → 后果*记录，让后来的贡献者不仅知道选了什么，还知道为什么。
- **以阶段为锚，不承诺日期。** 路线图围绕能力阶段（P0–P3）与出口标准组织，不做日历承诺。

## 参与文档贡献

设计文档是活的产物。若实现与设计出现偏差，请在同一个 Pull Request 中同步更新设计。对已有决策的实质性修改应追加新的 ADR 条目，而不是悄悄改写旧结论。
