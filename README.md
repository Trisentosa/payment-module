# PayGate

A payment gateway abstraction service built in Go. Handles payment initiation, status tracking, and webhook processing across multiple gateway providers (Midtrans, Doku, Stripe).

## Prerequisites

- [Go 1.23+](https://go.dev/dl/)
- [Docker Desktop](https://www.docker.com/products/docker-desktop/) (or any runtime with `docker compose` v2)
- [`golangci-lint`](https://golangci-lint.run/usage/install/) — for linting

## Getting Started

### 1. Clone and configure

```sh
git clone https://github.com/Trisentosa/payment-module.git
cd payment-module
cp .env.example .env
go mod tidy
```

The defaults in `.env.example` match the Docker Compose credentials — no edits needed for local dev.

### 2. Start infrastructure

```sh
make up       # starts Postgres, Redis, RabbitMQ in Docker
make migrate  # runs DB migrations
```

### 3. Run the service

```sh
make run      # go run ./cmd/server — restarts instantly on code changes
```

The service is now reachable at `http://localhost:8080`.

| Endpoint | Purpose |
|----------|---------|
| `GET /healthz` | Liveness probe |
| `GET /readyz` | Readiness probe (checks DB) |

> **Alternative — full Docker stack** (no Go toolchain needed):
> ```sh
> make up-full  # builds and runs the service container alongside infra
> make migrate
> ```

## Running Tests

```sh
make test
```

- **Domain unit tests** (`./internal/domain/...`) — no Docker required
- **Postgres integration tests** (`./internal/adapter/outbound/postgres/...`) — use [`testcontainers-go`](https://testcontainers.com/), no `make up` required

## Useful Commands

| Command | Description |
|---------|-------------|
| `make up` | Start infra containers (Postgres, Redis, RabbitMQ) |
| `make up-full` | Start full stack including service container |
| `make down` | Stop all containers |
| `make migrate` | Run DB migrations |
| `make test` | Run all tests with race detector |
| `make lint` | Run golangci-lint |

## Connecting to Local Infrastructure

**Postgres**
```sh
psql -h localhost -U paygate_user -d paygate
```
Or any GUI (TablePlus, DBeaver) at `localhost:5432`.

**RabbitMQ management UI** — http://localhost:15672 (`guest` / `guest`)

**Reset local DB**
```sh
docker compose down -v && make up && make migrate
```

## Project Structure

```
cmd/
  server/       — service entry point (wires dependencies, starts HTTP server)
  migrate/      — DB migration runner
internal/
  domain/       — business logic: aggregates, value objects, domain events, repository ports
  adapter/
    inbound/    — HTTP handlers and middleware
    outbound/   — PostgreSQL repository implementations
  infrastructure/ — config, logger, DB pool (no business logic)
  pkg/apperror/ — shared error taxonomy
migrations/     — SQL migration files (golang-migrate)
docs/           — architecture, conventions, ADRs, and more
```

For a deeper dive see [docs/architecture.md](docs/architecture.md) and [docs/local-dev.md](docs/local-dev.md).
