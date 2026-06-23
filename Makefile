.PHONY: up up-full down migrate sqlc openapi test lint build run check

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

lint:
	golangci-lint run ./...

build:
	go build ./...

# Gate: must pass before a task is considered done.
check: build lint test
