# Stage 1 — Builder
# go.mod and go.sum are copied first so that 'go mod download' is cached as a
# separate layer — a source-only change will not re-download dependencies.
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO_ENABLED=0 produces a fully static binary with no C runtime dependency,
# which is required for the distroless base image that has no libc.
RUN CGO_ENABLED=0 go build -o /paygate ./cmd/server

# Stage 2 — Runtime
# distroless/static contains no shell, no package manager, and no unnecessary
# OS tooling — only the binary is present.
FROM gcr.io/distroless/static-debian12
COPY --from=builder /paygate /paygate
ENTRYPOINT ["/paygate"]
