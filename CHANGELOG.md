# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/) and versions follow [SemVer](https://semver.org/) (v0.x until the P1 milestone completes — see [docs/03-roadmap.md](docs/03-roadmap.md)).

## [Unreleased]

### Added — P0 "Open-Source Ready" milestone

- **Routing & resilience** (`docs/design/01-routing-and-lb.md`): `RouterManager` with weighted candidate ordering (activates `AIProvider.Weight`), priority tiers, automatic retry/failover across providers (up to 3 attempts, nothing retried after bytes reach the client), Redis-shared circuit breaker (closed → open → half-open with probe slots), breaker-aware session affinity, breaker transition events persisted to `ai_gateway_router_events`.
- **Observability** (`docs/design/05-observability.md`): Prometheus metrics on a dedicated listener (`aigw_requests_total`, latency histograms, token counters, upstream attempts, failovers, breaker state), `/healthz` and `/readyz` (dependency pings, drain-aware), Grafana overview dashboard + compose observability profile.
- **Management-plane auth** (`docs/design/04-multi-tenancy-and-auth.md`): static admin bearer token (`system.admin_token` / `AIGW_ADMIN_TOKEN`) guarding all `/ai/gateway/*` routes; constant-time comparison; loud warning when unset.
- **Provider management API**: CRUD + `GET /ai/gateway/providers/health` with live breaker state; provider API keys encrypted (AES-256-GCM) on write.
- **Multi-database**: `database.driver` selects MySQL (default), PostgreSQL, or SQLite (demo tier).
- **Web console** (`frontend/`, `docs/design/08-web-console.md`): React + TypeScript SPA — login, dashboard with provider health, keys, providers, audit views; bilingual (en/zh); embedded into the binary at `/console/` via `embed.FS`.
- **Config**: `AIGW_*` environment overrides for all secrets and endpoints.
- **Engineering**: unit tests (breaker state machine, candidate ordering, pricing math, AES round-trip) running fully offline; GitHub Actions CI (vet, race tests, lint, frontend build, docker build) and release workflow (multi-platform binaries + GHCR multi-arch image); one-command docker-compose stack; issue/PR templates; CONTRIBUTING/SECURITY docs.

### Changed

- Repository restructured into `backend/` (Go) and `frontend/` (console) with docs at the root.
- `/ai/gateway/*` now requires the admin token when configured (**breaking** for previously open deployments — set `AIGW_ADMIN_TOKEN` and send it as a Bearer header).
- Upstream failures no longer propagate immediately: retryable errors (connect errors, 429/5xx before first byte) fail over to the next healthy provider.

### Documentation

- Full bilingual product & design suite under `docs/`: vision, gap analysis, phased roadmap (P0–P3), and ten design documents (routing, protocol adapters, billing, multi-tenancy, observability, security/guardrails, caching, console, extensibility/MCP, deployment).
