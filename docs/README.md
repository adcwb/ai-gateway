# ai-gateway Documentation

> 中文版文档见 [docs/zh-CN/](zh-CN/README.md) · Chinese documentation lives in [docs/zh-CN/](zh-CN/README.md)

**ai-gateway** is a self-hosted, OpenAI-compatible AI traffic control plane written in Go. It sits between your applications and LLM providers, giving you virtual key management, multi-dimensional quotas, audit logging, token accounting, billing, and multi-provider load balancing — from a single binary.

This directory contains the product planning and technical design suite that guides the project from its current state to a full-featured open-source AI gateway.

## How to read these documents

Start with the three top-level documents, in order. They establish what the product is, where it stands today, and how it gets to where it is going. The design documents under [design/](design/) are self-contained deep dives — read the ones relevant to the capability you care about.

### Top-level documents

| Document | What it covers |
| -------- | -------------- |
| [01 · Product Vision](01-product-vision.md) | Positioning, target users, competitive landscape, design principles |
| [02 · Gap Analysis](02-gap-analysis.md) | Honest inventory of current capabilities and what is missing, grounded in the actual codebase |
| [03 · Roadmap](03-roadmap.md) | Phased delivery plan (P0–P3) with exit criteria for each phase |

### Design documents

Each design document is tagged with the roadmap phase it belongs to and its dependencies on other designs.

| Document | Phase | What it covers |
| -------- | ----- | -------------- |
| [D01 · Routing & Load Balancing](design/01-routing-and-lb.md) | P0 | Routing strategies, weighted load balancing, failover, circuit breaking, health checks |
| [D02 · Protocol Adapters](design/02-protocol-adapters.md) | P2 | Bidirectional protocol translation (OpenAI / Anthropic / Gemini / Bedrock / Azure), usage normalization |
| [D03 · Billing & Monetization](design/03-billing-and-monetization.md) | P1–P2 | Pricing, balance accounts, ledger, subscriptions, payment gateways, invoicing |
| [D04 · Multi-Tenancy & Auth](design/04-multi-tenancy-and-auth.md) | P0–P2 | Tenant/project hierarchy, admin authentication, RBAC, SSO |
| [D05 · Observability](design/05-observability.md) | P0–P2 | Prometheus metrics, OpenTelemetry tracing, health endpoints, dashboards |
| [D06 · Security & Guardrails](design/06-security-and-guardrails.md) | P1–P2 | PII detection engine, guardrail pipeline, secret management |
| [D07 · Caching Strategies](design/07-caching-strategies.md) | P2 | Exact-match caching, semantic caching, cache-aware billing |
| [D08 · Web Console](design/08-web-console.md) | P1–P2 | Management console: information architecture, page designs, tech stack |
| [D09 · Extensibility](design/09-extensibility.md) | P3 | Plugin mechanism, hook points, MCP gateway, event bus |
| [D10 · Deployment & Operations](design/10-deployment-and-ops.md) | P0 | Deployment topologies, multi-database support, HA, open-source engineering |

## Document conventions

- **English is authoritative.** The Chinese mirror under [zh-CN/](zh-CN/README.md) is kept structurally parallel; when the two disagree, the English version wins.
- **Diagrams are Mermaid.** All architecture and flow diagrams use fenced ` ```mermaid ` blocks, which GitHub renders natively.
- **Designs reference real code.** When a design proposes changing existing behavior, it cites the actual file and function (e.g. `internal/biz/gateway.go`), verified against the repository at the time of writing.
- **ADR-style decisions.** Significant choices are recorded as *Context → Options → Decision → Consequences* so future contributors understand not just *what* was chosen but *why*.
- **Phases, not dates.** The roadmap is organized around capability phases (P0–P3) with exit criteria, not calendar promises.

## Contributing to these documents

Design documents are living artifacts. If an implementation diverges from a design, update the design in the same pull request. Substantial changes to a decision should append a new ADR entry rather than silently rewriting the old one.
