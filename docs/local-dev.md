# Local Development Setup

## Prerequisites

- Go 1.23+
- Docker Desktop (or equivalent with `docker compose` v2)
- `golangci-lint` — `brew install golangci-lint` or see https://golangci-lint.run/
- `golang-migrate` CLI (optional — `make migrate` uses `go run` instead)

## First-Time Setup

```sh
cp .env.example .env
# Edit .env if you need non-default credentials
```

## Mode A — Infrastructure in Docker, Service Runs Natively (Recommended)

Best for active development — code changes take effect immediately without a container rebuild.

```sh
make up        # starts Postgres, Redis, RabbitMQ in Docker
make migrate   # runs migrations against localhost:5432
make run       # go run ./cmd/server
```

Tail logs: stdout of `go run` directly.

## Mode B — Full Stack in Docker

For a clean end-to-end smoke test or onboarding without the Go toolchain.

```sh
make up-full   # builds and starts the service container + infra
make migrate   # runs migrations against the Docker network Postgres
```

Tail service logs: `docker compose logs -f paygate`

## Connecting to Postgres

```sh
psql -h localhost -U paygate_user -d paygate
```

Or via any GUI (TablePlus, DBeaver) at `localhost:5432`.

## RabbitMQ Management UI

http://localhost:15672 — credentials: `guest` / `guest`

## Reset Local DB

```sh
docker compose down -v && make up && make migrate
```

The `-v` flag drops the named volume, giving you a clean slate.

## Running Tests

```sh
make test
```

Domain unit tests (`go test ./internal/domain/...`) require no Docker.  
Integration tests (`go test ./internal/adapter/outbound/postgres/...`) use `testcontainers-go` to spin up an ephemeral Postgres automatically — no `make up` required.
