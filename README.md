# AridLink

AridLink is an operations backend for joint drought, desertification and land-degradation programs. It coordinates partner organizations, restoration sites, field monitoring, intervention work, evidence review, grant milestones, warnings, technology transfer and training. PostgreSQL transactions, optimistic versions, durable leases, an outbox and audit records preserve the cross-module invariants.

## Requirements

- Go 1.26
- PostgreSQL 17
- Docker Desktop for the Compose workflow
- Node.js 22 and npm 10 for the React client

## Run

Start PostgreSQL, then run the API:

```bash
docker compose up -d postgres
export ARIDLINK_DATABASE_URL='postgres://aridlink:aridlink@localhost:55432/aridlink?sslmode=disable'
go run ./cmd/api
```

The service applies numbered migrations and creates the initial platform administrator from `ARIDLINK_BOOTSTRAP_EMAIL` and `ARIDLINK_BOOTSTRAP_PASSWORD`. Defaults are for local development only. `GET /healthz` reports process health and `GET /readyz` verifies PostgreSQL connectivity.

Run the complete container stack with `docker compose up --build`. The API is then available on port `18080`.

## Configuration

| Variable | Purpose | Default |
| --- | --- | --- |
| `ARIDLINK_ADDRESS` | HTTP listen address | `:8080` |
| `ARIDLINK_DATABASE_URL` | PostgreSQL DSN | local port 5432 |
| `ARIDLINK_MIGRATIONS` | numbered migration directory | `migrations` |
| `ARIDLINK_SESSION_TTL` | authenticated session lifetime | `8h` |
| `ARIDLINK_WORKER_INTERVAL` | recovery and delivery cadence | `2s` |
| `ARIDLINK_WORKER_LEASE` | durable worker ownership window | `30s` |
| `ARIDLINK_WEBHOOK_URL` | optional event delivery endpoint | log sink |

## Business flows

Program managers activate a five-year program only after a partnership exists. They approve sites, plan monitoring cycles and interventions, and create funding milestones. Field officers collect observations, execute leased work orders and seal evidence. Technical reviewers conclude immutable evidence revisions, while finance reviewers reserve and disburse budget only for accepted evidence. Background workers expire alerts and reservations and deliver transactional outbox events.

Every authenticated write may include `Idempotency-Key`. Its scope includes organization, method and path, so unrelated tenants or operations cannot collide. A processing record is not automatically replayed after expiry because the business transaction may already have committed; it must be recovered deliberately.

## Web client

`web` uses React, TypeScript, Vite and Ant Design. It covers authentication, program overview, site and monitoring navigation, and operational queues. The Vite development server proxies `/v1`, `/healthz` and `/readyz` to `http://localhost:8080`.

```bash
npm --prefix web ci
npm --prefix web run dev
npm --prefix web test -- --run
npm --prefix web run typecheck
npm --prefix web run build
```

## Verification

Integration tests require an isolated PostgreSQL database. The test helper reads `ARIDLINK_TEST_DATABASE_URL`; when omitted it uses the Compose PostgreSQL on port `55432`. Tests create a unique schema per test and run the real migration against it.

```bash
docker compose up -d postgres
GOTOOLCHAIN=local go test ./... -count=1
GOTOOLCHAIN=local go test -race ./... -count=1
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go build ./...
```
