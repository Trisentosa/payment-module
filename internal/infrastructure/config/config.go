package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/Trisentosa/payment-module/internal/pkg/apikey"
)

type Config struct {
	Server   Server
	DB       DB
	Redis    Redis
	RabbitMQ RabbitMQ
	Midtrans Midtrans
	Log      Log
	OTel     OTel
	Admin    Admin
	Callers  Callers
}

type Server struct {
	Port        int
	MetricsPort int
}

type DB struct {
	Host     string
	Port     int
	Name     string
	User     string
	Password string
	MaxConns int
}

type Redis struct {
	Addr     string
	Password string
}

type RabbitMQ struct {
	URL string
}

type Midtrans struct {
	ServerKey string
	Env       string
	BaseURL   string
	TimeoutMs int
}

type Log struct {
	Level  string
	Format string
}

type OTel struct {
	Endpoint    string
	ServiceName string
}

type Admin struct {
	Key string
}

// Callers holds the services allowed to call the payments API.
type Callers struct {
	// KeyHashes maps the SHA-256 hex of each API key to the service it identifies.
	KeyHashes map[string]string
}

// minAdminKeyLength rejects trivially guessable admin keys.
const minAdminKeyLength = 32

func Load() (Config, error) {
	dbPort, err := strconv.Atoi(getEnv("DB_PORT", "5432"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid DB_PORT: %w", err)
	}
	dbMaxConns, err := strconv.Atoi(getEnv("DB_MAX_CONNECTIONS", "10"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid DB_MAX_CONNECTIONS: %w", err)
	}
	serverPort, err := strconv.Atoi(getEnv("SERVER_PORT", "8080"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid SERVER_PORT: %w", err)
	}
	metricsPort, err := strconv.Atoi(getEnv("METRICS_PORT", "9090"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid METRICS_PORT: %w", err)
	}
	midtransTimeoutMs, err := strconv.Atoi(getEnv("MIDTRANS_TIMEOUT_MS", "30000"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid MIDTRANS_TIMEOUT_MS: %w", err)
	}

	// Both secrets are required: without them the admin API or the payments API would
	// either be open to anyone or reject everyone.
	adminKey := os.Getenv("ADMIN_API_KEY")
	if len(adminKey) < minAdminKeyLength {
		return Config{}, fmt.Errorf("ADMIN_API_KEY must be set to at least %d characters", minAdminKeyLength)
	}
	callerKeys, err := apikey.ParseHashes(os.Getenv("CALLER_API_KEYS"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid CALLER_API_KEYS: %w", err)
	}
	if len(callerKeys) == 0 {
		return Config{}, fmt.Errorf("CALLER_API_KEYS must list at least one service:sha256 entry; generate one with `go run ./cmd/apikey <service>`")
	}

	midtransEnv := getEnv("MIDTRANS_ENV", "sandbox")
	midtransBaseURL := "https://api.sandbox.midtrans.com"
	if midtransEnv == "production" {
		midtransBaseURL = "https://api.midtrans.com"
	}

	return Config{
		Server: Server{Port: serverPort, MetricsPort: metricsPort},
		DB: DB{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     dbPort,
			Name:     getEnv("DB_NAME", "paygate"),
			User:     getEnv("DB_USER", "paygate_user"),
			Password: getEnv("DB_PASSWORD", "secret"),
			MaxConns: dbMaxConns,
		},
		Redis: Redis{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
		},
		RabbitMQ: RabbitMQ{
			URL: getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/paygate"),
		},
		Midtrans: Midtrans{
			ServerKey: getEnv("MIDTRANS_SERVER_KEY", ""),
			Env:       midtransEnv,
			BaseURL:   midtransBaseURL,
			TimeoutMs: midtransTimeoutMs,
		},
		Log: Log{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "text"),
		},
		OTel: OTel{
			Endpoint:    getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
			ServiceName: getEnv("OTEL_SERVICE_NAME", "paygate-service"),
		},
		Admin: Admin{
			Key: adminKey,
		},
		Callers: Callers{KeyHashes: callerKeys},
	}, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
