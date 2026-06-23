# OpenAPI Contract

PayGate uses a **spec-first** workflow. The YAML file is the single source of
truth; Go types and client SDKs are derived from it — never the other way around.

## File Locations

| File | Purpose |
|------|---------|
| `api/openapi.yaml` | Source of truth — edit this to change the contract |
| `api/oapi-codegen.yaml` | Generator config (package, output path) |
| `internal/adapter/inbound/http/openapi_gen.go` | Generated Go models — **do not edit manually** |

## Regenerating Go Models

Whenever you change `api/openapi.yaml`, regenerate and commit the Go output:

```sh
make openapi
```

This runs `oapi-codegen` (registered as a `go tool` in `go.mod`) and
overwrites `openapi_gen.go`. Commit both files together so the spec and the
generated code stay in sync.

## Browsing the Spec at Runtime

The running service serves the spec file at:

```
GET /openapi.yaml
```

Paste that URL into [Swagger UI](https://editor.swagger.io) or any OpenAPI
viewer to explore endpoints interactively.

## Generating a TypeScript Client (Frontend)

From the project root, with `openapi-typescript` installed globally:

```sh
npx openapi-typescript http://localhost:8080/openapi.yaml -o src/api/paygate.ts
```

Or against the committed YAML without a running server:

```sh
npx openapi-typescript api/openapi.yaml -o src/api/paygate.ts
```

Other generators (`openapi-generator-cli`, `orval`, etc.) accept the same
`api/openapi.yaml` path.

## Generating a Go Client (Service-to-Service)

In the consuming service, run oapi-codegen targeting the `client` template:

```sh
go tool oapi-codegen \
  -generate types,client \
  -package paygateclient \
  path/to/paygate/api/openapi.yaml \
  > paygate_client_gen.go
```

The generated client wraps `net/http` and uses the same model types.

## Adding or Changing an Endpoint

1. Edit `api/openapi.yaml` — add/modify the path, parameters, and schemas.
2. Run `make openapi` — updates `openapi_gen.go`.
3. Update the handler in `internal/adapter/inbound/http/` to use any new/changed
   types. The package is `http` (same as the generated file), so new types are
   immediately available.
4. Register the route in `cmd/server/main.go` if it is a new endpoint.
5. Commit `api/openapi.yaml` and `openapi_gen.go` together.

## Scope: What's in the Spec

Only **public API** endpoints belong in the spec:

| Endpoint | In spec? | Reason |
|----------|----------|--------|
| `POST /payments` | Yes | Caller-facing |
| `DELETE /payments/{id}` | Yes | Caller-facing |
| `POST /webhooks/midtrans` | **No** | Gateway-to-service callback; Midtrans owns the shape |
| `GET /healthz`, `GET /readyz` | **No** | Infrastructure probes |
| `GET /openapi.yaml` | **No** | Meta-endpoint |

## Idempotency Key Scoping

The spec documents that `POST /payments` is idempotent on
`(X-Service-Name, reference_id)`. The `X-Service-Name` header is **required**
on all mutating requests — omitting it will prevent correct idempotency scoping
even if the server does not currently enforce its presence at the HTTP layer.

## Error Contract

All errors follow the `ErrorResponse` schema:

```json
{ "code": "INVALID_INPUT", "message": "reference_id is required" }
```

The `code` values map 1-to-1 to `apperror.Code` constants. See
[error-handling.md](error-handling.md) for the full table.
