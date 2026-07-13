# ai-gateway

> 中文说明见 [README.zh-CN.md](README.zh-CN.md)

A self-hosted, OpenAI-compatible **AI traffic control plane** written in Go. One binary puts virtual keys, quotas, audit logging, token accounting, cost tracking, and multi-provider load balancing between your applications and every LLM you use.

**Docs:** [Product vision](docs/01-product-vision.md) · [Gap analysis](docs/02-gap-analysis.md) · [Roadmap](docs/03-roadmap.md) · [Design suite](docs/README.md)

<p align="center">
  <img src="docs/assets/homepage-en.png" alt="ai-gateway public homepage" width="49%">
  <img src="docs/assets/dashboard.png" alt="ai-gateway console dashboard" width="49%">
</p>

## Features

- **Virtual key management** — issue `sk-vk-*` credentials (AES-256-GCM at rest, SHA-256 lookup); rotate upstream keys without touching clients
- **Multi-provider routing** — four per-key strategies (weighted / priority / least-latency / least-cost), automatic retry/failover with per-mapping fallback chains and a per-attempt audit trail, Redis-backed circuit breaker shared by all instances, opt-in active health probes for idle-period breaker recovery ([design](docs/design/01-routing-and-lb.md))
- **Protocol adapters** — outbound Anthropic, Gemini, Azure OpenAI and Bedrock (Claude/Titan/Llama/Mistral/Nova, hand-rolled SigV4); inbound OpenAI Chat, Anthropic Messages (`/anthropic/v1/messages`) and Responses API (`/ai/v1/responses`, with `previous_response_id`/`store` server-side conversation state); full SSE streaming translation both directions ([design](docs/design/02-protocol-adapters.md))
- **Multi-tenancy** — tenant → project → key hierarchy with a zero-config default tenant, project quota templates; cost attribution per tenant/project/key/model ([design](docs/design/04-multi-tenancy-and-auth.md))
- **Auth & access control** — bootstrap admin token, OIDC/SSO login (JIT provisioning + claim→role mapping), 4-role RBAC (owner/admin/member/viewer), admin API keys, operator audit log ([design](docs/design/04-multi-tenancy-and-auth.md))
- **Balance billing** — opt-in prepaid/postpaid accounts per tenant, double-entry ledger, freeze→settle deduction on the proxy path, grace-period suspension, budget alerts (webhook), sell-side price tables decoupled from upstream cost ([design](docs/design/03-billing-and-monetization.md))
- **Multi-dimensional quotas** — daily/hourly tokens, request counts, concurrency slots, credit budgets, tool-call quota for MCP; per-model overrides; atomic Redis Lua enforcement
- **Token accounting & reports** — usage parsed from every response (incl. streaming and cached tokens), priced per model, rolled up daily for dashboards and chargeback
- **Guardrails** — pluggable per-policy checker chain (rule-based PII detection, prompt-injection signatures, topic fencing, external gRPC checker), block / redact / log, both non-streaming and streaming outbound scanning (best-effort on streams), audit-body AES-GCM encryption ([design](docs/design/06-security-and-guardrails.md))
- **Response caching** — exact-match cache plus semantic (embedding-similarity) cache with a pluggable vector backend (Redis/RediSearch), synthetic stream replay, and configurable hit billing (free/discount/full) ([design](docs/design/07-caching-strategies.md))
- **MCP gateway** — proxies Streamable HTTP MCP tool traffic (batched JSON-RPC, GET/SSE push, POST) behind the same `sk-vk-*` virtual keys as models, with per-key tool whitelists and guardrail scanning on arguments/results ([design](docs/design/09-extensibility.md))
- **Batch + Files API proxy** — passthrough for `openai_compatible` providers with shadow bookkeeping and deferred usage settlement at the upstream batch discount
- **Extensibility** — `pre_request`/`post_response` hooks (compile-time, webhook, or WASM via `wazero`) and an event bus (`on_audit`/`on_billing`) with durable log + webhook/Kafka sinks ([design](docs/design/09-extensibility.md))
- **Audit logging** — every request recorded (tokens, latency, guardrail action, client metadata) with batched async writes, optional Elasticsearch indexing, session grouping
- **Observability** — Prometheus `/metrics` on a dedicated listener (incl. Go runtime/process/DB-pool collectors), `/healthz` + `/readyz` probes, Grafana dashboard shipped in-repo, opt-in OpenTelemetry tracing ([design](docs/design/05-observability.md))
- **Web console** — React SPA embedded in the binary at `/console/`: dashboard/usage charts, keys, providers with live breaker state, model mappings (fallback-chain drag editor), guardrail policies (checker-chain builder), audit (body/session/security views), tenants, billing, users & admin keys, settings — maintained under [`frontend/`](frontend/)
- **Public homepage** — static HTML/CSS/JS marketing page (no build step) served at `/`, embedded the same way as the console — maintained under [`homepage/`](homepage/)
- **Multi-database** — MySQL (default), PostgreSQL, SQLite (demo); session affinity, model mapping, IP whitelisting, L1/L2 key caching

## Repository layout

```text
├── backend/    # Go gateway (Kratos): proxy, quotas, audit, routing, console embed
├── frontend/   # React + TypeScript web console (Vite)
├── homepage/   # public marketing page, plain HTML/CSS/JS, no build step — served at "/"
├── docs/       # Product & design documentation (EN + zh-CN)
├── deploy/     # docker-compose stack, Prometheus & Grafana provisioning
└── Dockerfile  # Multi-stage: console build → Go build → runtime image
```

## Quick start

### docker compose (recommended)

```bash
git clone https://github.com/adcwb/ai-gateway.git
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

> **Local development:** keep real credentials out of git — copy `config.yaml` to `configs/config.local.yaml` (gitignored) and run `./server -conf configs/config.local.yaml`, or use the `AIGW_*` env vars.

## API surface

- **Proxy** (`Authorization: Bearer sk-vk-*`): `GET /ai/v1/models`, `POST /ai/v1/chat/completions`, `POST /ai/v1/embeddings`, `POST /ai/v1/rerank`, `POST /ai/v1/responses`, plus passthrough for other `/ai/v1/*` routes and the Batch/Files endpoints. OpenAI-compatible; no breaking changes as a standing guarantee. Providers registered with `providerType: anthropic`, `gemini`, `azure_openai` or `bedrock` are translated transparently. `POST /anthropic/v1/messages` accepts native Anthropic-shaped requests. `/ai/mcp/{serverName}` proxies MCP tool-call traffic under the same virtual keys.
- **Management** (`Authorization: Bearer <admin token>`, or an admin API key / SSO session under RBAC):
  - Keys: CRUD + reveal, quota config/usage, per-key cache config, PII-policy binding, tool whitelist
  - Providers: CRUD + `GET /ai/gateway/providers/health` (live breaker state), model sync
  - Model mappings & guardrail policies: CRUD (fallback chains, checker chains)
  - Tenancy: `POST|GET /ai/gateway/tenants`, `POST|GET /ai/gateway/projects` (quota templates)
  - Billing: `POST /ai/gateway/billing/recharge`, `PUT /ai/gateway/billing/account`, `GET /ai/gateway/billing/ledger`
  - Reports: `GET /ai/gateway/stats/overview`, `GET /ai/gateway/stats/timeseries`
  - Audit: list / sessions / security-overview
  - Users, admin API keys, SSO/OIDC config, extensions (hooks/event-bus sinks)
- **Ops** (no auth): `GET /healthz`, `GET /readyz`, and `GET /metrics` on the metrics listener.

## Status & roadmap

Implemented against the [roadmap](docs/03-roadmap.md) — P0 through the protocol-surface, MCP-gateway/extensibility, and homepage rounds are all shipped. See the [feature status table in `CLAUDE.md`](CLAUDE.md#feature-status-what-exists-vs-what-doesnt) for the authoritative, per-capability breakdown of what's done vs. partial vs. designed-only.

### Not yet implemented (designed — see the [design suite](docs/README.md))

| Area | Missing pieces |
| --- | --- |
| Access control ([D04](docs/design/04-multi-tenancy-and-auth.md)) | Tenant-scoped filtering across every list/read endpoint (RBAC today only gates the named state-changing actions) |
| Protocols ([D02](docs/design/02-protocol-adapters.md)) | Tool-calling/multimodal for the 4 newer Bedrock model families, console UI for provider `adapter_config`/bedrock credentials |
| Billing commerce ([D03](docs/design/03-billing-and-monetization.md)) | Payment gateways (Stripe/Alipay/WeChat), subscription plans, invoices, email alert channel |
| Security ([D06](docs/design/06-security-and-guardrails.md)) | LLM-judge escalation, `topic_fence` embedding-similarity mode |
| Extensibility ([D09](docs/design/09-extensibility.md)) | Console UI for `ai_extensions` and breaker/quota bus events; Anthropic Message Batches API translation (Batch/Files proxy covers `openai_compatible` providers only) |

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
