# Go Coding Conventions

## Package Names
- Singular, no suffix: `payment`, `postgres`, `http`, `apperror`
- Never: `payments`, `repository`, `handler`, `utils`

## Receiver Names
- Single-letter abbreviation of the type: `p *Payment`, `r *PaymentRepo`

## Error Handling
- Domain and application layer: always return `*apperror.AppError` via constructors (`apperror.NotFound(...)`, etc.)
- Infrastructure/adapter: wrap with `apperror.Internal("context", err)`
- Never `fmt.Errorf` without `%w` for wrapping
- Never return a plain `errors.New` from domain/app layer

## Context
- Always the first parameter: `func (r *Repo) Save(ctx context.Context, ...)`
- Never store context in a struct

## Logging
- Never log in the domain layer
- Always use `logger.FromContext(ctx)` — never `slog.Default()` directly
- Required fields per log event: `payment_id`, `trace_id`, `caller_service`

## Test Files
- Co-located with source: `aggregate_test.go` next to `aggregate.go`
- Package: `payment_test` (external test package) for domain; `postgres_test` for adapter

## Imports
- Group: stdlib, then external, then internal — separated by blank lines
- Use full import paths: `github.com/trisentosa/paygate/internal/...`
