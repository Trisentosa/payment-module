# PayGate Service — Claude Context

This file is the entry point for AI-assisted development in this repository.
All conventions, design decisions, and rules live in `docs/` — this file
only points to them. Read the relevant doc before working on each area.

## Where to Look

| Topic | File |
|-------|------|
| Architecture & principles | [docs/architecture.md](docs/architecture.md) |
| Domain model & aggregates | [docs/domain-model.md](docs/domain-model.md) |
| Coding conventions (Go) | [docs/conventions.md](docs/conventions.md) |
| Database schema & migrations | [docs/database.md](docs/database.md) |
| Error handling | [docs/error-handling.md](docs/error-handling.md) |
| Logging | [docs/logging.md](docs/logging.md) |
| API contract & OpenAPI workflow | [docs/openapi.md](docs/openapi.md) |
| Event schema (RabbitMQ) | [docs/events.md](docs/events.md) |
| Testing strategy | [docs/testing.md](docs/testing.md) |
| Local dev setup | [docs/local-dev.md](docs/local-dev.md) |
| Security | [docs/security.md](docs/security.md) |
| ADRs | [docs/adr/](docs/adr/) |

## Definition of Done

Before reporting any coding task as complete, run:

```
make check
```

This runs `build → lint → test` in order. All three must pass with zero errors. Do not skip or work around failures — fix the root cause.

## Ground Rules

1. Never return a plain `error` from the domain or application layer — always wrap with `apperror`.
2. Never log inside the domain layer — log at the adapter boundary only.
3. Never call `slog.Default()` directly — always use `logger.FromContext(ctx)`.
4. All DB writes that emit domain events MUST do so in a single transaction.
5. No foreign keys in the database — see [docs/database.md](docs/database.md).
