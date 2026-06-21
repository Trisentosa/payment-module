# Database

## Schema Overview

| Table | Purpose |
|---|---|
| `payments` | Core payment aggregate |
| `payment_attempts` | Per-attempt audit trail (retry reconciliation) |
| `payment_events` | Domain event store (audit log) |
| `outbox` | Transactional outbox for RabbitMQ publishing (TD-02) |
| `idempotency_keys` | Idempotency cache (TD-02) |

## Migration Convention

Files live in `migrations/` and follow the pattern: `NNN_verb_noun.up.sql` / `NNN_verb_noun.down.sql`

Run via: `make migrate` (calls `go run ./cmd/migrate/main.go up`)

## No Foreign Keys

FKs are intentionally omitted. Rationale:
- Simplifies schema evolution without coordinating FK migrations
- Avoids lock contention on parent rows under high insert load
- Referential integrity is enforced at the application layer (service/repo)

## Soft Delete Pattern

All queries against `payments` must include `WHERE deleted_at IS NULL` (already enforced in `PaymentRepo`).

## Index Naming

`idx_{table}_{columns}` — e.g., `idx_payments_status`, `idx_payment_events_aggregate`

## Connecting Locally

```
psql -h localhost -U paygate_user -d paygate
```

Or via any GUI (TablePlus, DBeaver) at `localhost:5432`.
