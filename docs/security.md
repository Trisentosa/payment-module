# Security

## Who can call what

| Surface | Credential | Checked by |
|---|---|---|
| `/payments/**` (payments, refunds) | Per-service API key: `Authorization: Bearer pgk_…` | `middleware.CallerAuth` |
| `/api/v1/admin/**` | Shared ops key: `X-Admin-Key` | `middleware.AdminAuth` |
| `/webhooks/midtrans` | Midtrans signature in the body (SHA-512 with the server key) | `midtrans.Adapter.ParseWebhook` |
| `/healthz`, `/readyz`, `/openapi.yaml` | none | — |
| `/metrics` (port 9090) | none — keep it off the public network | — |

## Caller API keys

Each calling service has its own key. The key **is** the service's identity: PayGate
stores which service a key belongs to, and every payment a service creates is owned by
it. A service can only read, cancel and refund its own payments; anyone else's answer
404, exactly as if the payment didn't exist, so services can't probe each other.

`X-Service-Name` no longer proves anything. It is accepted for compatibility, but if sent
it must name the key's own service (otherwise 403).

### Issuing a key

```sh
go run ./cmd/apikey order-service
```

This prints the key (give it to the service as a secret; it is shown once) and a
`service:sha256` entry for PayGate's `CALLER_API_KEYS`. PayGate only ever stores the
SHA-256 hash, so a leaked config file doesn't leak usable keys. A lost key can't be
recovered; issue a new one.

### Rotating a key

A service may have several entries at once:

1. Issue a new key and **add** its entry to `CALLER_API_KEYS`; restart PayGate.
2. Switch the service to the new key.
3. Remove the old entry; restart PayGate.

### Configuration

| Variable | Required | Format |
|---|---|---|
| `CALLER_API_KEYS` | yes | `service:sha256hex,service:sha256hex,…` |
| `ADMIN_API_KEY` | yes, ≥ 32 characters | plain string |

PayGate refuses to start if either is missing or malformed, instead of silently running
an open or closed API.

## Card data

PayGate never receives card numbers. Callers' frontends tokenize cards with Midtrans.js
and send PayGate only the one-time `card_token`.
