# Contributing to ai-gateway

Thanks for considering a contribution. This document covers the practical conventions; the *why* behind the architecture lives in [docs/](docs/README.md).

## Repository layout

- `backend/` — Go gateway (Kratos framework, Wire DI, GORM, Redis)
- `frontend/` — React + TypeScript web console (Vite)
- `docs/` — product & design documentation (English authoritative, `docs/zh-CN/` mirror)
- `deploy/` — docker-compose, Prometheus, Grafana

## Getting started

```bash
# backend: tests run fully offline (miniredis + in-memory SQLite)
cd backend
go test ./...
go build ./cmd/server

# frontend
cd frontend
npm install
npm run dev        # dev server on :5173, /ai proxied to :8080
```

## Backend conventions (enforced in review)

These follow the Kratos layering rules described in `CLAUDE.md`:

1. **Layering** — `biz` holds business rules and never imports `service`/`data`; `service` only decodes/encodes HTTP; `data` only touches DB/Redis.
2. **Errors** — business errors are `kerrors` sentinels in `internal/biz/errors.go` (`kerrors.NotFound("REASON", "msg")`), never `fmt.Errorf` strings across layer boundaries. Service handlers use `failWithErr`.
3. **Wire DI** — every new component gets a `NewXxx` constructor added to the relevant `ProviderSet`; run `wire ./cmd/server` and commit `wire_gen.go`.
4. **Migrations are additive** — new columns/tables only; register models in `data.autoMigrate`. Destructive changes require a design-doc decision first.
5. **Hot-path budget** — nothing on the proxy path may add blocking I/O; heavy work goes through async workers (see `AuditWorker`).
6. **Redis keys** — prefix `ai:gw:`, parameterized keys as `const ...Fmt` format strings.
7. **Tests** — new biz logic ships with unit tests. Use `miniredis` for Redis behavior and in-memory SQLite (`glebarez/sqlite`) for DB behavior.

## Frontend conventions

- The console is a pure client of the documented management API — **no private endpoints**.
- Keep dependencies minimal; discuss before adding a UI library.
- All user-facing strings go through `src/i18n.ts` with both `en` and `zh` values.

## Docs discipline

If your change diverges from a design document in `docs/design/`, update the design in the same PR. Significant decision changes append a new ADR entry (Context → Options → Decision → Consequences) rather than rewriting the old one.

## Pull requests

- Keep PRs focused; separate refactors from behavior changes.
- CI must be green: `go vet`, `go test -race`, `golangci-lint`, frontend build, docker build.
- Reference the design doc or issue your change implements.

## Reporting security issues

Do **not** open a public issue — see [SECURITY.md](SECURITY.md).
