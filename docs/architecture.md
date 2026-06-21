# Architecture

PayGate follows **hexagonal architecture** (ports & adapters). The dependency rule flows strictly inward: adapters depend on the domain, never the reverse.

## Layer Map

```
cmd/server          — wires dependencies, starts HTTP server
  └─ internal/
       ├─ domain/payment/          — aggregate, value objects, events, repository port
       ├─ adapter/
       │    ├─ inbound/http/       — HTTP handlers, middleware
       │    └─ outbound/postgres/  — PostgreSQL repository implementation
       ├─ infrastructure/          — config, logger, db pool (no domain logic)
       └─ pkg/apperror/            — shared error taxonomy
```

## What Each Layer Owns

| Layer | Owns | Forbidden |
|---|---|---|
| Domain | Business rules, state transitions, domain events | Logging, DB, HTTP |
| Application (future) | Orchestration use cases | Direct DB/HTTP calls |
| Adapter (inbound) | HTTP parsing, response serialization | Business logic |
| Adapter (outbound) | SQL queries, JSON mapping | Business logic |
| Infrastructure | DB pool, logger, config | Domain types |

## Key Decisions

- No DI framework — manual wiring in `cmd/server/main.go`
- No ORM — raw pgx v5 queries for full control and predictable performance
- No FK constraints — enforced at application layer; see [database.md](database.md)
- Domain events written atomically with the aggregate in one transaction; publishing deferred to TD-02 outbox
