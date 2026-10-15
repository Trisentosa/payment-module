.PHONY: up up-full down migrate sqlc openapi test test-integration e2e e2e-up e2e-down lint build run check

# Mode A: infrastructure only (service runs natively)
up:
	docker compose up -d

# Mode B: full stack including the service container
up-full:
	docker compose --profile full up -d --build

down:
	docker compose down

# Run the service locally (Mode A). Requires 'make up' first.
run:
	go run ./cmd/server

migrate:
	go run ./cmd/migrate/main.go up

sqlc:
	go tool sqlc generate

# Regenerate Go models from api/openapi.yaml.
# Run this whenever api/openapi.yaml changes; commit the result.
openapi:
	go tool oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml

test:
	go test ./... -race -count=1

test-integration:
	go test -tags=integration ./... -race -count=1

# End-to-end: the real server binary against Postgres, Redis and RabbitMQ in Docker
# (own ports, see test/e2e/docker-compose.yml) and a fake Midtrans.
E2E_COMPOSE = docker compose -f test/e2e/docker-compose.yml

e2e-up:
	$(E2E_COMPOSE) up -d --wait

e2e-down:
	$(E2E_COMPOSE) down

e2e: e2e-up
	go test -tags=e2e ./test/e2e/ -count=1 -v

lint:
	golangci-lint run ./...

build:
	go build ./...

# Gate: must pass before a task is considered done. (make e2e is separate: it needs Docker.)
check: build lint test
