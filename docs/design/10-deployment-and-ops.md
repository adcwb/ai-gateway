# D10 · Deployment, Operations & Open-Source Engineering

> [中文版](../zh-CN/design/10-deployment-and-ops.md) · Part of the [ai-gateway documentation suite](../README.md)

| | |
| --- | --- |
| **Phase** | P0 (compose, PostgreSQL, tests, CI, docs) · P3 (Helm/operator, SQLite demo) |
| **Depends on** | [D05 Observability](05-observability.md) (probes/dashboards ship in the deploy artifacts) |
| **Depended on by** | every other design — this document is the credibility layer that makes them adoptable |

## Context

Today: a Dockerfile, a Makefile, `configs/config.yaml`, and a compiled `server.exe` in the repo root. No compose file, no CI, no tests (zero `*_test.go` in the tree), MySQL-only, Chinese-only docs, and `go.mod` housekeeping issues (module-name drift noted in CLAUDE.md; a binary committed to git). For an open-source infra project, time-to-first-request and visible engineering hygiene *are* the top of the funnel — this document is P0 not because it is glamorous but because nothing else ships without it.

## Deployment topology matrix

| Tier | Stack | State | Target user |
| --- | --- | --- | --- |
| **Demo** (P3) | single binary + SQLite + in-memory quota fallback | one file | "kick the tires in 60 seconds" — no Redis: quotas/breakers degrade to per-instance in-memory with a startup warning |
| **Standard** (P0) | `docker compose up`: gateway + MySQL (or PG) + Redis (+ optional Prometheus/Grafana profile) | volumes | evaluation → small production |
| **Production HA** (P0 docs, P3 Helm) | N gateway replicas behind an LB; managed DB + Redis; ES optional | external | platform teams |

### HA statement (make it explicit, keep it true)

The gateway is stateless by design: all coordination state is in Redis (quota windows via Lua, concurrency ZSETs, breaker state, cache), all durable state in the DB, cross-instance cache invalidation via the existing `ai:gw:key:invalidate` pub/sub. Therefore: any replica serves any request; rolling upgrades = drain via `readyz` flip ([D05](05-observability.md)); the failure domains are Redis (quotas/breakers/billing-gate degrade — fail-open per design principle 6, with loud metrics) and the DB (management plane down; proxy path survives on caches until TTL). Each new feature's design must state which side of this line its state lives on — that is a review checklist item.

### Kubernetes (P3)

Helm chart under `deploy/helm/`: Deployment + HPA (CPU + `aigw_concurrency_slots`-based custom metric optional), PDB, probes wired to `/healthz`//`readyz`, secrets for encryption key & DB/Redis DSNs, optional ServiceMonitor. An **operator is explicitly deferred** beyond the chart until there is a CRD-shaped need (e.g. declarative tenant/provider provisioning at fleet scale) — a chart covers the P3 exit criterion (3-replica HA passing the failover test).

## Multi-database support (P0: PostgreSQL · P3: SQLite)

### Decision (ADR)

- **Context:** MySQL-only halves the audience; GORM already abstracts most of the dialect surface.
- **Decision:** officially support MySQL 8 + PostgreSQL 15+ from P0, SQLite for the demo tier only (never production-documented). `database.driver` config key selects the GORM driver.
- **Compatibility audit checklist** (what actually differs in this codebase):
  - `gorm.io/datatypes.JSON` — maps to `json`/`jsonb`/`text` per driver; JSON-path *queries* must go through `datatypes.JSONQuery` instead of raw `->>` SQL (audit any hand-written JSON predicates).
  - `AUTO_INCREMENT` vs sequences, `datetime` precision, index length limits on `varchar` uniqueIndex columns (`key_hash`), and case-sensitivity of `LIKE` (PG) — covered by keeping schema declarations in GORM tags only, no raw DDL.
  - Raw SQL sweep: any `db.Raw`/`db.Exec` strings need dialect review (the audit-session aggregation queries in `ListAuditSessions` are the likely spot).
- **Migrations:** `autoMigrate` stays the mechanism while roadmap invariant 2 (additive-only) holds. The moment a destructive change is unavoidable, adopt versioned migrations (golang-migrate) — recorded now as the trigger condition so it's a plan, not a debate.
- **Consequences:** CI runs the full suite against both engines (matrix job); SQLite runs a smoke subset.

## Testing strategy (P0)

Pyramid, pragmatic about the existing zero-test baseline:

1. **Unit (`internal/biz`)** — the target of the ≥ 60% P0 coverage gate. Priority order = risk order: `quota.go` Lua behavior (via miniredis), `router.go` strategies/breaker ([D01](01-routing-and-lb.md)), `credits.go` pricing math, `key_cache.go` invalidation, guardrail chain semantics. Repo interfaces per Kratos layering make DB fakes cheap; Redis behavior tests use miniredis where Lua support suffices, falling back to the dockerized integration tier where it doesn't.
2. **Integration (`test/integration`)** — docker-compose-backed (testcontainers-go): real MySQL/PG/Redis, httptest fake providers. Owns the flow tests every design doc lists: failover, two-tenant leakage, billing freeze/settle invariants, cache cross-dialect, upgrade-from-snapshot migration.
3. **E2E** — Playwright console flows ([D08](08-web-console.md)) + scripted OpenAI-SDK/Anthropic-SDK client runs against the compose stack.

## CI/CD (GitHub Actions, P0)

| Workflow | Trigger | Jobs |
| --- | --- | --- |
| `ci.yml` | PR + main | `go vet` + `golangci-lint` → unit tests (race) with coverage gate → integration matrix {mysql, postgres} → `wire ./cmd/server` && `git diff --exit-code` (generated-code freshness) → console build + Playwright (path-filtered) |
| `release.yml` | tag `v*` | goreleaser: multi-arch binaries (linux/amd64+arm64, darwin, windows) + docker buildx multi-arch push + SBOM + checksums + changelog draft |
| `nightly.yml` | cron | payment-sandbox tests (credential-gated), dependency audit (`govulncheck`), docker base refresh |

Versioning: SemVer; `v0.x` until P1 completes, `v1.0` = P1 exit criteria met (the API-stability promise then covers `/ai/v1/*` *and* the management API envelope).

## Repository hygiene (P0, one-time)

- Fix `go.mod` module name to `github.com/opscenter/ai-gateway` (per CLAUDE.md standing note); remove `server.exe` from git and `.gitignore` build outputs.
- Root docs: rewrite `README.md` in English (badges, 60-second quickstart via compose, feature matrix, links into `docs/`), add `README.zh-CN.md`, `CONTRIBUTING.md` (build, wire, test, PR conventions — largely extractable from CLAUDE.md), `SECURITY.md` (private disclosure channel, supported versions), `CODE_OF_CONDUCT.md`, issue/PR templates, `CHANGELOG.md` (keep-a-changelog).
- License: MIT already in place — confirm headers policy (none required) and third-party notices for the console build.
- **OpenAPI spec** (`api/openapi.yaml`) for the management API: hand-maintained initially (Kratos HTTP here is hand-routed, not proto-generated), validated in CI by contract tests, and the source for the console's generated client ([D08](08-web-console.md)) — which is what keeps it honest.

## Configuration & operations polish (P0)

- All config keys overridable via env (`AIGW_` prefix mapping) — compose/k8s ergonomics; secrets (encryption key, DSNs, admin token) documented env-first.
- Config validation at startup with actionable errors (32-byte key check exists; extend to DSN/addr sanity, admin-token-empty warning in non-dev mode).
- Graceful-shutdown drain order documented and tested: readyz→503, HTTP drain, worker queue flush (audit/billing), close.
- `server doctor` subcommand: checks DB/Redis connectivity, Redis version features (vector for [D07](07-caching-strategies.md)), encryption-key validity, pending migrations — the first thing support asks users to run.

## Touched code

| Location | Change |
| --- | --- |
| `deploy/compose/docker-compose.yml` (+ grafana/prometheus profile), `deploy/helm/` (P3) | new |
| `.github/workflows/`, `.golangci.yml`, `.goreleaser.yml` | new |
| `internal/data/data.go` | driver selection; dialect audit fixes |
| `cmd/server/main.go` | `doctor`, `rekey` subcommands; env overrides |
| `go.mod`, `.gitignore`, root docs, `api/openapi.yaml` | hygiene set above |
| `test/integration/` (new) | testcontainers suites |

## Verification

This document's deliverables *are* the P0 exit criteria ([Roadmap](../03-roadmap.md)): compose-to-first-request under 10 minutes on a clean machine following README only; CI green with coverage gate on both databases; release workflow produces installable multi-arch artifacts from a tag. The meta-verification: run the quickstart on a machine that has never seen the repo, with a stopwatch.
