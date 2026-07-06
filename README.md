# ai-gateway

> 中文说明见 [README.zh-CN.md](README.zh-CN.md)

A self-hosted, OpenAI-compatible **AI traffic control plane** written in Go. One binary puts virtual keys, quotas, audit logging, token accounting, cost tracking, and multi-provider load balancing between your applications and every LLM you use.

**Docs:** [Product vision](docs/01-product-vision.md) · [Gap analysis](docs/02-gap-analysis.md) · [Roadmap](docs/03-roadmap.md) · [Design suite](docs/README.md)

## Features

- **Virtual key management** — issue `sk-vk-*` credentials (AES-256-GCM at rest, SHA-256 lookup); rotate upstream keys without touching clients
- **Multi-provider routing** — four per-key strategies (weighted / priority / least-latency / least-cost), automatic retry/failover with per-mapping fallback chains and a per-attempt audit trail, Redis-backed circuit breaker shared by all instances ([design](docs/design/01-routing-and-lb.md))
- **Protocol adapters** — call Anthropic, Gemini (full request/response/SSE translation) and Azure OpenAI from OpenAI-compatible clients, with normalized usage accounting ([design](docs/design/02-protocol-adapters.md))
- **Multi-tenancy** — tenant → project → key hierarchy with a zero-config default tenant; cost attribution per tenant/project/key/model ([design](docs/design/04-multi-tenancy-and-auth.md))
- **Balance billing** — opt-in prepaid/postpaid accounts per tenant, double-entry ledger, freeze→settle deduction on the proxy path, grace-period suspension, budget alerts, sell-side price tables decoupled from upstream cost ([design](docs/design/03-billing-and-monetization.md))
- **Multi-dimensional quotas** — daily/hourly tokens, request counts, concurrency slots, credit budgets; per-model overrides; atomic Redis Lua enforcement
- **Token accounting & reports** — usage parsed from every response (incl. streaming and cached tokens), priced per model, rolled up daily for dashboards and chargeback
- **PII guardrails** — rule-based detection engine (CN resident ID with checksum, mobile, bank card Luhn, email, API secrets) plus prompt-injection signatures; block / redact / log per policy ([design](docs/design/06-security-and-guardrails.md))
- **Response caching** — exact-match cache with normalized keys, synthetic stream replay, and configurable hit billing (free/discount/full) ([design](docs/design/07-caching-strategies.md))
- **Audit logging** — every request recorded (tokens, latency, PII action, client metadata) with batched async writes, optional Elasticsearch indexing, session grouping
- **Observability** — Prometheus `/metrics` on a dedicated listener, `/healthz` + `/readyz` probes, Grafana dashboard shipped in-repo ([design](docs/design/05-observability.md))
- **Web console** — React SPA embedded in the binary at `/console/` (dashboard with usage, keys, providers with live breaker state, audit, tenants, billing) — maintained under [`frontend/`](frontend/)
- **Management-plane auth** — static admin token (`AIGW_ADMIN_TOKEN`) guarding all `/ai/gateway/*` endpoints
- **Multi-database** — MySQL (default), PostgreSQL, SQLite (demo); session affinity, model mapping, IP whitelisting, L1/L2 key caching

## Repository layout

```text
├── backend/    # Go gateway (Kratos): proxy, quotas, audit, routing, console embed
├── frontend/   # React + TypeScript web console (Vite)
├── docs/       # Product & design documentation (EN + zh-CN)
├── deploy/     # docker-compose stack, Prometheus & Grafana provisioning
└── Dockerfile  # Multi-stage: console build → Go build → runtime image
```

## Quick start

### docker compose (recommended)

```bash
git clone https://github.com/opscenter/ai-gateway.git
cd ai-gateway/deploy/compose
docker compose up -d
# with Prometheus + Grafana:
docker compose --profile observability up -d
```

The gateway listens on `:8080` (proxy + management + console) and `:9090` (metrics). **Change `AIGW_ADMIN_TOKEN` and `AIGW_ENCRYPTION_KEY` in `docker-compose.yml` before any real use.**

### From source

```bash
# backend only (console shows a placeholder page)
cd backend && go build -o server ./cmd/server && ./server -conf configs/config.yaml

# full build: console + embed + server
make all && make run
```

### First request

```bash
ADMIN="Authorization: Bearer change-this-admin-token"

# 1. register an upstream provider
curl -X POST localhost:8080/ai/gateway/providers -H "$ADMIN" -H 'Content-Type: application/json' -d '{
  "name": "openai", "baseUrl": "https://api.openai.com/v1",
  "apiKey": "sk-your-upstream-key",
  "models": [{"name": "gpt-4o-mini", "is_default": true}]
}'

# 2. create a virtual key (response contains the sk-vk-* plaintext — shown once)
curl -X POST localhost:8080/ai/gateway/key -H "$ADMIN" -H 'Content-Type: application/json' \
  -d '{"name": "demo", "providerId": 1}'

# 3. call it like OpenAI
curl localhost:8080/ai/v1/chat/completions \
  -H "Authorization: Bearer sk-vk-..." -H 'Content-Type: application/json' \
  -d '{"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "hello"}]}'
```

Open `http://localhost:8080/console/` and sign in with the admin token.

## Configuration

`backend/configs/config.yaml`, every key overridable via environment:

| Env | Purpose |
| --- | --- |
| `AIGW_HTTP_ADDR` / `AIGW_METRICS_ADDR` | listeners (default `:8080` / `:9090`) |
| `AIGW_DB_DRIVER` / `AIGW_DB_DSN` | `mysql` (default), `postgres`, `sqlite` |
| `AIGW_REDIS_ADDR` / `AIGW_REDIS_PASSWORD` | Redis |
| `AIGW_ENCRYPTION_KEY` | **exactly 32 bytes** — encrypts virtual keys & provider keys at rest |
| `AIGW_ADMIN_TOKEN` | management API bearer token; empty = open (dev only, warning logged) |

Tables are created automatically on startup (additive GORM auto-migration).

## API surface

- **Proxy** (`Authorization: Bearer sk-vk-*`): `GET /ai/v1/models`, `POST /ai/v1/chat/completions`, `POST /ai/v1/embeddings`, `POST /ai/v1/rerank`, plus passthrough for other `/ai/v1/*` routes. OpenAI-compatible; no breaking changes as a standing guarantee. Providers registered with `providerType: anthropic`, `gemini` or `azure_openai` are translated transparently.
- **Management** (`Authorization: Bearer <admin token>`):
  - Keys: CRUD + reveal, quota config/usage
  - Providers: CRUD + `GET /ai/gateway/providers/health` (live breaker state)
  - Tenancy: `POST|GET /ai/gateway/tenants`, `POST|GET /ai/gateway/projects`
  - Billing: `POST /ai/gateway/billing/recharge`, `PUT /ai/gateway/billing/account`, `GET /ai/gateway/billing/ledger`
  - Reports: `GET /ai/gateway/stats/overview`, `GET /ai/gateway/stats/timeseries`
  - Audit: list / sessions / security-overview
- **Ops** (no auth): `GET /healthz`, `GET /readyz`, and `GET /metrics` on the metrics listener.

## Status & roadmap

Implemented against the [roadmap](docs/03-roadmap.md):

- **P0 — open-source ready**: weighted LB + failover + circuit breaking, metrics/probes, admin auth, encrypted provider keys, multi-DB, tests + CI, compose stack, console MVP.
- **P1 — commercial loop (core)**: tenant→project→key hierarchy, opt-in prepaid/postpaid balance accounts with double-entry ledger and freeze→settle deduction, grace-period suspension, budget alerts, sell-side price tables, daily usage attribution + reports, rule-based PII engine (block/redact/log).
- **P2 — differentiation (core)**: native Anthropic and Gemini outbound adapters (incl. SSE stream translation) and Azure OpenAI adapter with normalized usage, exact-match response cache with cache-aware billing.
- **Gap-closing round**: routing strategies + fallback chains + latency EWMA, provider model sync, project quota templates, billing alert webhook, `doctor`/`rekey` CLI, OpenAPI spec, Helm chart, CI coverage gate + PostgreSQL smoke, console management UI (key creation with show-once secret, provider forms, usage charts).

### Not yet implemented (designed — see the [design suite](docs/README.md))

| Area | Missing pieces |
| --- | --- |
| Access control ([D04](docs/design/04-multi-tenancy-and-auth.md)) | SSO/OIDC (user system intentionally skipped — SSO will be introduced directly), tenant-scoped management queries |
| Routing ([D01](docs/design/01-routing-and-lb.md)) | Active health probes (passive breaker only today) |
| Billing commerce ([D03](docs/design/03-billing-and-monetization.md)) | Payment gateways (Stripe/Alipay/WeChat), subscription plans, invoices, email alert channel |
| Protocols ([D02](docs/design/02-protocol-adapters.md)) | Inbound Anthropic Messages & OpenAI Responses endpoints, Bedrock outbound adapter (SigV4), Batch & Files API proxying |
| Security ([D06](docs/design/06-security-and-guardrails.md)) | Pluggable guardrail checker chain, external PII engine (gRPC/Presidio), output-stream scanning, audit-body encryption |
| Caching ([D07](docs/design/07-caching-strategies.md)) | Semantic cache (vector-backed), caching of streaming responses, cache flush API |
| Observability ([D05](docs/design/05-observability.md)) | OpenTelemetry tracing |
| Console ([D08](docs/design/08-web-console.md)) | Model & price-table pages, audit body/session viewers, settings page, Playwright E2E |
| Future ([D09](docs/design/09-extensibility.md)) | Plugin/hook system, event bus, MCP gateway |

Contributions welcome — each row has a full technical design behind it.

## Development

```bash
cd backend
go test ./...        # unit tests (miniredis + in-memory SQLite; no services needed)
go vet ./...
wire ./cmd/server    # regenerate DI after changing ProviderSets

cd ../frontend
npm run dev          # console dev server on :5173, proxying /ai to :8080
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for conventions and [SECURITY.md](SECURITY.md) for vulnerability reporting.

## License

[MIT](LICENSE)
