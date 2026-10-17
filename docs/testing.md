# Testing

## Commands

| Command | Runs | Needs |
|---|---|---|
| `make test` | All unit tests, with `-race` | Go only |
| `make check` | `build → lint → test`; the definition of done | Go, golangci-lint |
| `make test-integration` | Unit tests plus everything tagged `integration` | Docker, or a scratch Postgres (below) |
| `make e2e` | The end-to-end suite (below) | Docker |
| `make sandbox` | The sandbox drill: real Midtrans transactions (below) | Docker, sandbox keys, cloudflared, a person |

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

## Sandbox Drill

`make sandbox` runs the same harness against the **real Midtrans sandbox** (tag
`sandbox`, `test/e2e/sandbox_test.go`). It creates real sandbox transactions of every
method, pays them, waits for Midtrans's real notification, then refunds or cancels and
checks PayGate against Midtrans's own status API.

Paying is automated where Midtrans's simulator allows it. The simulators are plain HTML
forms with no API, and `simulator_test.go` submits them like a browser: virtual accounts
(BCA, BNI, Permata, BRI), ShopeePay (PIN `123456`), and QRIS paid with GoPay, DANA, OVO
or LinkAja. Cards need a real browser for 3-D Secure 2, so the drill prints the link and
waits for you to enter OTP `112233`. GoPay itself is manual too.

The drill runs PayGate with `MIDTRANS_QRIS_ACQUIRER="airpay shopee"`
(`SANDBOX_QRIS_ACQUIRER` changes it). On sandbox accounts where GoPay isn't fully active,
GoPay payments and GoPay-acquired QRIS codes can be created but never paid ("Transaction
failed to be processed" in the simulator, whichever app pays), while ShopeePay-acquired
codes work with every app. `SANDBOX_MANUAL=1` makes
every payment manual, e.g. if Midtrans changes the simulator pages.

Setup, once:

1. In the sandbox dashboard, Settings → Access Keys, use the **General** credentials (not
   BI SNAP). Put them in `.env`, which git ignores:
   `MIDTRANS_SERVER_KEY=SB-Mid-server-…` and `MIDTRANS_CLIENT_KEY=SB-Mid-client-…`
   (the client key only tokenizes test cards). Production keys are refused.
2. Install `cloudflared`. The drill opens a quick tunnel (no account) and has PayGate send
   `X-Override-Notification` with each charge, so the dashboard's notification URL is left
   alone. With your own tunnel, set `SANDBOX_PUBLIC_URL` to the URL forwarding to
   `localhost:18080` instead.

```sh
make sandbox                                  # every scenario, about 10 minutes of your time
make sandbox SANDBOX_RUN=TestSandbox_QRIS     # one scenario
SANDBOX_NO_WEBHOOKS=1 make sandbox            # no tunnel: only the scenarios no one pays in
```

`SANDBOX_WAIT` (default `10m`) is how long each step waits for a payment.

Midtrans refunds from the merchant's payable balance. A sandbox account without one
declines every refund with `414 merchant insufficient funds`; the drill then checks that
PayGate recorded the decline cleanly and skips the rest of that scenario. Card refunds may
also be declined until Midtrans settles the capture (next day in production).

## Writing Tests

- Name the behavior, not the function: `TestRefund_UnknownOutcomeStaysPendingAndReserved`.
- Regression tests say what broke in a comment above the test.
- Assert on stored state (`fake.PaymentRepo.FindByID`) rather than on the value a handler
  returned: the stored copy is what the next request sees.
- Table tests for one rule with many inputs; separate tests for separate behaviors.
