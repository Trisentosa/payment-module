# Database

## Schema Overview

| Table | Purpose |
|---|---|
| `payments` | Core payment aggregate |
| `payment_attempts` | Per-attempt audit trail (retry reconciliation) |
| `payment_events` | Domain event store (audit log) |
| `outbox_events` | Transactional outbox for RabbitMQ publishing |
| `idempotency_keys` | Idempotency cache (currently unused; idempotency lives in Redis) |
| `refunds` | Refunds of a payment, saved together with it (part of the Payment aggregate) |

## Optimistic Locking

`payments.version` is bumped on every save, and a save only succeeds if the row still has
the version it was loaded with. Otherwise the repository returns `CONFLICT` (HTTP 409) and
the caller must reload and retry. This is what stops two concurrent refunds from exceeding
the amount paid, and a webhook and a cancel from overwriting each other.

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
