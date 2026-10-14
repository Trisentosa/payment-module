# Architecture

PayGate follows **hexagonal architecture** (ports & adapters). The dependency rule flows strictly inward: adapters depend on the application and domain layers, never the reverse.

## Layer Map

```
cmd/server             — wires dependencies, starts the HTTP server and background workers
cmd/migrate            — applies migrations/
cmd/apikey             — issues a caller API key and prints its hash
internal/
  ├─ domain/payment/        — Payment aggregate (with its Refunds), statuses, events, repository port
  ├─ application/
  │    ├─ command/          — use cases that change state: create, cancel, refund, callback, reconcile
  │    ├─ query/            — read use cases: get, list, admin views
  │    └─ ports/            — interfaces the application needs: gateway, cache, publisher, attempts, outbox
  ├─ adapter/
  │    ├─ inbound/http/     — handlers, middleware (auth, idempotency, logging), generated OpenAPI models
  │    └─ outbound/
  │         ├─ postgres/    — repository, outbox writer/worker, expiry job, attempt recorder (sqlc in db/)
  │         ├─ gateway/midtrans/ — Midtrans Core API adapter (charge, status, cancel, refund, webhooks)
  │         ├─ rabbitmq/    — event publisher (publisher confirms)
  │         └─ redis/       — cache TTL policy
  ├─ infrastructure/        — config, logger, db pool, cache client, telemetry (no domain logic)
  ├─ pkg/                   — apperror (error taxonomy), apikey (key generation and hashing)
  └─ testsupport/fake/      — in-memory port implementations for tests; never wired into the server
```

## What Each Layer Owns

| Layer | Owns | Forbidden |
|---|---|---|
| Domain | Business rules, state transitions, domain events | Logging, DB, HTTP |
| Application | Use cases: load the aggregate, call ports, save | SQL, HTTP, gateway wire formats |
| Adapter (inbound) | HTTP parsing, auth, idempotency, response serialization | Business logic |
| Adapter (outbound) | SQL, gateway API calls, message publishing, JSON mapping | Business logic |
| Infrastructure | DB pool, cache client, logger, config | Domain types |

## Gateways

Use cases depend on `ports.GatewayPort`, never on Midtrans. A gateway declares per payment
method what it supports (`Capability`: charge, refund, partial refund), and the application
checks that before calling it. Adding a gateway means one new adapter plus one entry in the
gateway factory in `cmd/server`; use cases don't change.

A gateway call that ends without a usable answer returns an error wrapping
`ports.ErrUnknownOutcome`. The application then leaves the payment or refund open instead of
guessing, and the reconciler settles it. See [database.md](database.md#unknown-gateway-outcomes-and-reconciliation)
and [ADR-001](adr/001-synchronous-gateway-calls.md).

## Background Workers

All run as goroutines in the server process. Each takes a Redis lock per pass where two
replicas must not do the same work.

| Worker | Every | Does |
|---|---|---|
| Outbox worker | 5 s | Publishes pending `outbox_events` rows to RabbitMQ. Rows are claimed in a transaction (`FOR UPDATE SKIP LOCKED`); failures back off 30 s, 60 s, ... and become `FAILED` after 6 tries |
| Expiry job | 60 s | Moves PENDING payments past `expired_at` to EXPIRED |
| Reconciler | 60 s | Settles INITIATED payments and PENDING refunds whose gateway reply was lost |

## Key Decisions

- No DI framework — manual wiring in `cmd/server/main.go`.
- sqlc for queries — SQL in `internal/adapter/outbound/postgres/queries/`, generated Go in `db/` (never edit by hand). pgxpool directly for transactions.
- No FK constraints — enforced in the application layer; see [database.md](database.md).
- Domain events are written in the same transaction as the aggregate (`payment_events` and `outbox_events`) and published afterwards by the outbox worker, so a saved change is never lost and never announced without being saved. Delivery is at least once; see [events.md](events.md).
- Optimistic locking: `payments.version` is checked on every save. A stale save fails with `CONFLICT`, so a webhook and an API call can't overwrite each other.
- Spec-first HTTP contract: [openapi.md](openapi.md).
