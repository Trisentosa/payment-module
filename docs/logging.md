# Logging

## Setup

Uses `log/slog` (Go 1.21+ stdlib). Format is controlled by `LOG_FORMAT` env var:
- `text` — human-readable (local dev default)
- `json` — structured (staging/prod)

Logger is initialized once in `cmd/server/main.go` and set as `slog.Default()`.

## Context Propagation

The request logger middleware enriches a logger with per-request fields and attaches it to the context:

```go
// Enrich
l := base.With("trace_id", ..., "caller_service", ..., "method", ..., "path", ...)
ctx := logger.WithContext(r.Context(), l)

// Retrieve anywhere downstream
log := logger.FromContext(ctx)
log.Info("payment saved", "payment_id", p.ID)
```

**Rule:** Never call `slog.Default()` directly — always `logger.FromContext(ctx)`.

## Required Fields per Event

| Field | Source |
|---|---|
| `trace_id` | `X-Trace-Id` request header |
| `caller_service` | `X-Service-Name` request header |
| `payment_id` | Set when operating on a payment |

## What NOT to Log

- Raw gateway credentials or API keys
- Card numbers, CVVs, or any PCI-scoped data
- Full request/response bodies from the gateway (log only status codes and IDs)
- Anything in the domain layer — domain has no logger dependency
