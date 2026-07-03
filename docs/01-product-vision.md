# 01 · Product Vision

> [中文版](zh-CN/01-product-vision.md) · Part of the [ai-gateway documentation suite](README.md)

## One-sentence positioning

**ai-gateway is a self-hosted AI traffic control plane: a single Go binary that puts virtual keys, quotas, audit, billing, and multi-provider routing between your applications and every LLM you use.**

The word *control plane* is deliberate. A proxy forwards traffic; a control plane governs it. The value of this project is not in relaying HTTP requests — it is in everything that happens around the relay: who may call which model, how much they may spend, what they actually consumed, what it cost, what was sent and returned, and what happens when a provider degrades.

## Why this project exists

Organizations adopting LLMs hit the same wall in the same order:

1. **Week 1** — developers embed provider API keys directly in applications. It works.
2. **Month 1** — keys leak into repos, nobody knows which team is spending what, and switching providers means touching every application.
3. **Month 3** — finance asks for cost attribution per team, security asks for an audit trail of what was sent to external models, and engineering asks for automatic failover because a single provider outage took down every AI feature at once.

Every serious LLM adopter ends up building an internal gateway. ai-gateway is that gateway, built once, properly, in the open.

## Target users

Three user archetypes drive the requirements. Every roadmap item should serve at least one of them; features serving none of them are out of scope.

### 1. The platform team (enterprise internal cost control)

A platform/infrastructure team at a company where many internal teams consume LLMs. They need:

- Virtual keys per team/project with independent quotas and spending limits
- Cost attribution and chargeback reports ("Team A spent ¥40k on gpt-4o last month")
- A compliance-grade audit trail of prompts and completions (with PII controls)
- Provider failover so one vendor's outage doesn't cascade
- Self-hosted, because prompts cannot leave their network perimeter

### 2. The API reseller / aggregator

An operator who buys capacity from upstream providers and resells it to their own customers, with a margin. They need everything the platform team needs, plus:

- Prepaid balance accounts with recharge, deduction, and out-of-credit suspension
- Customer-facing pricing that differs from upstream cost (tiered prices, group discounts)
- Subscription plans, payment gateway integration, invoices
- Hard multi-tenant isolation between customers

### 3. The SaaS product team (embedded LLM features)

A product team shipping LLM-powered features inside their own SaaS. They need:

- Per-end-user rate limiting and abuse protection via short-lived virtual keys
- Model mapping so the product refers to `our-summarizer-v2` while the backend swaps real models freely
- Session affinity for prompt-cache savings
- Latency-first routing and semantic caching to control unit economics

## Competitive landscape

The AI gateway space is crowded but stratified. The honest comparison:

| Capability | ai-gateway (target) | LiteLLM | One API / New API | Portkey | Kong AI Gateway | Higress | Helicone |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Language / deploy | Go, single binary | Python | Go, single binary | SaaS (+ self-host gateway) | Lua/Go plugin on Kong | Go/Envoy | SaaS (+ self-host) |
| OpenAI-compatible proxy | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ (observability-first) |
| Multi-provider protocol translation | 🎯 P2 | ✅ broad | 🟡 partial | ✅ | 🟡 | 🟡 | ➖ |
| Weighted LB + failover + circuit breaking | 🎯 P0 | 🟡 retry/fallback | 🟡 channel retry | ✅ | ✅ (gateway-grade) | ✅ (gateway-grade) | ➖ |
| Virtual keys + quotas | ✅ today | ✅ | ✅ | ✅ | 🟡 | 🟡 | 🟡 |
| **Full commercial billing loop** (balance, ledger, plans, payments, invoices) | 🎯 P1–P2 | ➖ (budgets only) | 🟡 (balance, no payments/invoices) | ➖ | ➖ | ➖ | ➖ |
| **Compliance-grade audit** (bodies, sessions, PII actions, ES search) | ✅ today | 🟡 logging | 🟡 logs | ✅ | 🟡 | 🟡 | ✅ (core focus) |
| Multi-tenancy + RBAC | 🎯 P1 | ✅ teams | 🟡 | ✅ | ✅ (Kong RBAC) | 🟡 | ✅ |
| Guardrails / PII | 🎯 P1–P2 | 🟡 hooks | ➖ | ✅ | ✅ plugins | 🟡 | 🟡 |
| Web console | 🎯 P1 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |

Legend: ✅ mature · 🟡 partial · ➖ absent · 🎯 planned (phase)

### Where the open space is

Reading the matrix column by column reveals the gap this project fills:

- **LiteLLM** is the broadest translator but is Python (operationally heavier at high concurrency) and stops at budgets — no commercial billing loop.
- **One API / New API** proved the demand for a Go single-binary gateway with balance-based resale in the Chinese ecosystem, but audit, guardrails, observability, and engineering rigor (tests, docs) are thin.
- **Portkey / Helicone** are excellent but SaaS-first; the fully self-hosted story is secondary.
- **Kong / Higress** are gateway-grade at L7 but treat AI as a plugin; virtual-key economics, billing, and LLM-native audit are not their center of gravity.

**The unoccupied position: a self-hosted Go single binary that combines gateway-grade traffic management with a complete commercial billing loop and compliance-grade audit.** That is the position ai-gateway targets. Archetype 2 (resellers) has no first-class open-source option today; archetypes 1 and 3 must currently assemble 2–3 tools to cover what one gateway should do.

## Design principles

These principles resolve day-to-day design disputes. When two designs conflict, the one that honors more of these wins.

1. **OpenAI compatibility first.** The `/ai/v1/*` surface is the contract. Any client that speaks OpenAI must work unmodified. Native protocols (Anthropic, Gemini) are additive entrances, never replacements.
2. **Single binary, minimal dependencies.** `./server -conf config.yaml` with MySQL/PostgreSQL + Redis must be a complete production deployment. Elasticsearch, vector stores, and payment gateways are strictly optional and degrade gracefully when absent.
3. **Stateless and horizontally scalable.** All shared state lives in the database and Redis. Any instance can serve any request; killing an instance loses nothing. (The existing L1 cache + Redis pub/sub invalidation pattern in `internal/biz/key_cache.go` is the template.)
4. **Headless first.** Every capability is an API before it is a screen. The web console is a client of the management API, with zero private endpoints.
5. **The hot path is sacred.** The proxy path budget is single-digit milliseconds of added latency. Anything heavier — audit persistence, billing settlement, PII deep scans — happens asynchronously (the existing `AuditWorker` batching pattern is the template).
6. **Fail open on economics, fail closed on security.** If the billing or quota subsystem is degraded, configurable policy decides whether traffic passes (default: pass, reconcile later). If authentication or a blocking guardrail is degraded, traffic stops.
7. **Everything metered is auditable.** Any number that appears on an invoice must be traceable to individual audit log records. Billing without provenance is a support nightmare.
8. **Design for the protocol we haven't seen yet.** Adapters, hooks, and normalization layers are interfaces, not switch statements. The LLM API landscape mutates quarterly (Responses API, MCP, agentic tool calls); the architecture must absorb new shapes without core rewrites.

## What ai-gateway is not

Scope discipline is a feature:

- **Not an inference server.** It never runs models; it governs traffic to things that do (including self-hosted vLLM/Ollama — which are just OpenAI-compatible providers).
- **Not a prompt-engineering platform.** No prompt versioning, A/B testing, or playgrounds. Observability data is exportable to tools that do this.
- **Not a general-purpose API gateway.** No ambition to replace Kong/Nginx for non-AI traffic. If you have an existing gateway, ai-gateway sits behind it happily.
- **Not an agent framework.** It will govern and audit agent traffic (P3: MCP gateway), but building agents belongs to client-side frameworks.

## Success criteria (24-month horizon)

- A platform team can go from `git clone` to a production deployment with failover and dashboards in under one hour.
- A reseller can operate a paid API business (recharge → consume → invoice) using only this project.
- A security officer can answer "what did we send to external models last quarter, and was PII involved?" from the console alone.
- The community ships new provider adapters without touching core code.
