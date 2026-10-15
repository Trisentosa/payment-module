# Testing

## Commands

| Command | Runs | Needs |
|---|---|---|
| `make test` | All unit tests, with `-race` | Go only |
| `make check` | `build → lint → test`; the definition of done | Go, golangci-lint |
| `make test-integration` | Unit tests plus everything tagged `integration` | Docker, or a scratch Postgres (below) |
| `make e2e` | The end-to-end suite (below) | Docker |

## Layers

| Layer | How it's tested | Example |
|---|---|---|
| Domain | Plain unit tests, no fakes needed | `domain/payment/refund_test.go` |
| Application | Use cases against the fakes in `internal/testsupport/fake` | `application/command/reconcile_test.go` |
| Inbound HTTP | Real handlers and middleware on fakes, through `httptest` | `adapter/inbound/http/payment_handler_test.go` |
| Midtrans adapter | Against an `httptest.Server` replying with payloads from Midtrans's docs | `adapter/outbound/gateway/midtrans/*_test.go` |
| Postgres adapter | Integration: the real migrations against a real Postgres | `adapter/outbound/postgres/*_test.go` |
| RabbitMQ publisher | Integration: a RabbitMQ container | `adapter/outbound/rabbitmq/event_publisher_test.go` |

## Fakes

`internal/testsupport/fake` implements the ports in memory. They behave like the real
adapters where a test could otherwise pass for the wrong reason:

- `PaymentRepo` stores copies (so a test can't change stored state by holding a pointer),
  enforces the `(reference_id, caller_service)` unique key and optimistic locking, keeps the
  events each save consumed in `Events`, and lists in the same order as the SQL.
- `Gateway` is scripted through fields (`CreateErr`, `RefundResult`, `State`, ...) and
  records calls (`RefundCalls`, `StatusCalls`, ...). `OnRefund` runs during `CreateRefund`,
  to simulate a webhook racing the reply.
- `Cache` ignores TTLs; set `Err` to simulate Redis being down.
- `AttemptRecorder` keeps attempts; `Of(op)` filters by operation.

When a port gains a method, add it to the fake so it keeps matching the real adapter.

## Integration Tests

Files start with `//go:build integration` and are skipped by `make test`.

**Postgres.** `setupTestDB` starts a `postgres:16-alpine` container through
testcontainers, then applies the real `migrations/`. Without Docker, point
`PAYGATE_TEST_DATABASE_URL` at a scratch database instead. **Each test drops and recreates
its `public` schema**, so never use a database you care about:

```sh
# One-off scratch Postgres, no Docker (adjust the binary path to your version)
initdb -D /tmp/pg -U postgres --auth=trust
pg_ctl -D /tmp/pg -o "-p 55432 -c listen_addresses=127.0.0.1 -c unix_socket_directories=''" -l /tmp/pg.log start
createdb -h 127.0.0.1 -p 55432 -U postgres paygate_test

PAYGATE_TEST_DATABASE_URL="postgres://postgres@127.0.0.1:55432/paygate_test?sslmode=disable" \
  go test -tags integration -count=1 ./internal/adapter/outbound/postgres/
```

The Postgres tests share that database, so they must not call `t.Parallel()`.

**RabbitMQ.** Needs Docker; there is no external-broker override yet.

## End-to-End Tests

`test/e2e` (tag `e2e`) builds the real server binary and runs it the way production does:
real Postgres, Redis and RabbitMQ from `test/e2e/docker-compose.yml`, migrations applied
by `cmd/migrate`. Only Midtrans is faked: `fake_midtrans_test.go` answers charge, status,
cancel and refund calls with Midtrans's documented shapes, and sends signed notifications
to PayGate's webhook. The tests then drive PayGate over HTTP as a calling service would and
check the result at every edge: API responses, events on RabbitMQ, what the gateway was
asked to do, and rows in Postgres.

```sh
make e2e        # starts the stack (ports 55433, 56379, 55672), runs the suite
make e2e-down   # stops it; the database lives in tmpfs, so nothing is kept
```

Each run resets the schema and flushes Redis first. The reconciler runs every second in
the suite (`RECONCILE_*` variables), so lost-reply flows settle in seconds. The fake's
behavior is chosen by the payment's `reference_id`: one containing `LOST` gets its charge
reply after PayGate's timeout, one containing `DENY` is declined. On failure the suite
prints the end of the server's log.

Unit tests cover rules; the e2e suite covers wiring. Add an e2e test when a behavior
depends on how parts are put together (routing, config, transactions, the outbox), not for
every rule.

## Writing Tests

- Name the behavior, not the function: `TestRefund_UnknownOutcomeStaysPendingAndReserved`.
- Regression tests say what broke in a comment above the test.
- Assert on stored state (`fake.PaymentRepo.FindByID`) rather than on the value a handler
  returned: the stored copy is what the next request sees.
- Table tests for one rule with many inputs; separate tests for separate behaviors.
