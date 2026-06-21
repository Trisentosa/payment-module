package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Server   Server
	DB       DB
	Redis    Redis
	RabbitMQ RabbitMQ
	Midtrans Midtrans
	Log      Log
}

type Server struct {
	Port int
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
}

type Log struct {
	Level  string
	Format string
}

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

	return Config{
		Server: Server{Port: serverPort},
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
			Env:       getEnv("MIDTRANS_ENV", "sandbox"),
		},
		Log: Log{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "text"),
		},
	}, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
