# Error Handling

## Taxonomy (`internal/pkg/apperror`)

| Code | HTTP Status | When to Use |
|---|---|---|
| `NOT_FOUND` | 404 | Resource does not exist |
| `ALREADY_EXISTS` | 409 | Duplicate creation attempt |
| `INVALID_INPUT` | 400 | Bad request data from caller |
| `INVALID_STATE` | 422 | State transition not allowed |
| `GATEWAY_ERROR` | 502 | External gateway call failed |
| `IDEMPOTENCY_CONFLICT` | 409 | Same idempotency key, different payload |
| `INTERNAL_ERROR` | 500 | Unexpected internal failure |

## Usage by Layer

**Domain layer** — use typed constructors, no cause:
```go
return apperror.InvalidInput("reference_id is required")
return apperror.InvalidState("can only mark PENDING from INITIATED")
```

**Adapter/infrastructure layer** — wrap with context:
```go
return apperror.Internal("insert payment", err)
return apperror.GatewayError("midtrans charge failed", err)
```

**HTTP handler** — call `middleware.WriteError(w, err)`; it translates to status code automatically.

## Checking Error Codes

```go
if apperror.IsCode(err, apperror.CodeNotFound) { ... }
```

## What NOT to Do

- `return errors.New("something went wrong")` — use `apperror.*`
- `return fmt.Errorf("failed")` — use `apperror.Internal("context", err)`
- Logging the error in multiple layers — log once at the HTTP boundary
