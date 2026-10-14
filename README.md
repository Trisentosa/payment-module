# PayGate

A payment gateway abstraction service built in Go. Other services create, cancel and refund payments through one API; PayGate talks to the gateway, tracks status from its webhooks, and announces every change on RabbitMQ. Midtrans is the only gateway implemented; the gateway port is built for more.

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

The service is now reachable at `http://localhost:8080`. Payment endpoints need a caller
API key; `.env.example` configures a local-only one:

```sh
curl -H "Authorization: Bearer pgk_local-dev-only-do-not-use-in-prod" localhost:8080/payments
```

Issue real keys with `go run ./cmd/apikey <service-name>` (see [docs/security.md](docs/security.md)).

| Endpoint | Purpose |
|----------|---------|
| `GET /healthz` | Liveness probe |
| `GET /readyz` | Readiness probe (checks Postgres only; Redis and RabbitMQ always report ok for now) |
| `GET /openapi.yaml` | The API contract ([docs/openapi.md](docs/openapi.md)) |

> **Alternative — full Docker stack** (no Go toolchain needed):
> ```sh
> make up-full  # builds and runs the service container alongside infra
> make migrate
> ```

## Running Tests

```sh
make test               # unit tests, no Docker needed
make test-integration   # adds Postgres and RabbitMQ tests (needs Docker)
```

Without Docker, the Postgres tests can run against a scratch database; see [docs/testing.md](docs/testing.md).

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
cmd/            — server, migrate, apikey (issues caller API keys)
internal/
  domain/       — the Payment aggregate, its refunds, statuses and events
  application/  — use cases (command/, query/) and the ports they need
  adapter/      — HTTP in; Postgres, Midtrans, RabbitMQ, Redis out
  infrastructure/ — config, logger, DB pool, cache client (no business logic)
  pkg/          — apperror, apikey
migrations/     — SQL migration files (golang-migrate)
api/            — openapi.yaml, the API contract
docs/           — architecture, domain model, events, testing, ADRs, and more
```

For a deeper dive see [docs/architecture.md](docs/architecture.md), [docs/domain-model.md](docs/domain-model.md) and [docs/local-dev.md](docs/local-dev.md).
