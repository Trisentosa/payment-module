.PHONY: up up-full down migrate test lint run

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

test:
	go test ./... -race -count=1

lint:
	golangci-lint run ./...
