# Database

## Schema Overview

| Table | Purpose |
|---|---|
| `payments` | Core payment aggregate |
| `payment_attempts` | Every outbound gateway call for a payment (charge, cancel, refund, status check) with its outcome: SUCCEEDED, FAILED or UNKNOWN. Written outside the payment's transaction so it survives a failed save |
| `payment_events` | Domain event store (audit log) |
| `outbox_events` | Transactional outbox for RabbitMQ publishing |
| `idempotency_keys` | Idempotency cache (currently unused; idempotency lives in Redis) |
| `refunds` | Refunds of a payment, saved together with it (part of the Payment aggregate) |

## Unknown Gateway Outcomes and Reconciliation

A gateway call can end with no usable answer (timeout, dropped connection, 5xx). The
call may or may not have taken effect, so PayGate never guesses:

- **Charge**: the payment is stored as `INITIATED` (it is otherwise never stored in that
  state) and the caller gets 502 naming the payment ID.
- **Refund**: the refund stays `PENDING`, with its amount still reserved.

The reconciler (`command.Reconciler`, every minute, one replica at a time through a
Redis lock) asks the gateway what happened:

| Waiting since | What the gateway says | Result |
|---|---|---|
| `INITIATED` > 2 min | no such transaction | `FAILED` (`CHARGE_NOT_CREATED`) |
| | settled / captured | `COMPLETED` |
| | failed | `FAILED` |
| | still pending | cancelled at the gateway, then `FAILED` (`CHARGE_REPLY_LOST`): the caller never got the payment instructions |
| refund `PENDING` > 5 min | lists the refund key | refund `SUCCEEDED` |
| refund `PENDING` > 30 min | doesn't list it | re-sent with the same refund key (the gateway won't refund twice) |
| refund `PENDING` > 6 days | — | logged as needing manual review (Midtrans only retries a refund key for 7 days) |

The times above are the defaults. `RECONCILE_INTERVAL`, `RECONCILE_CHARGE_GRACE`,
`RECONCILE_REFUND_GRACE` and `RECONCILE_REFUND_RETRY_AFTER` change them (Go durations such
as `90s`); the charge grace must exceed `MIDTRANS_TIMEOUT_MS`, or the server refuses to start.

A webhook that arrives first settles the payment the same way; the reconciler then
finds nothing to do. The expiry job leaves `INITIATED` payments to the reconciler.

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
