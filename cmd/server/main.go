package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	httpAdapter "github.com/Trisentosa/payment-module/internal/adapter/inbound/http"
	httpMiddleware "github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/adapter/outbound/gateway"
	"github.com/Trisentosa/payment-module/internal/adapter/outbound/gateway/midtrans"
	postgresAdapter "github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres"
	redisAdapter "github.com/Trisentosa/payment-module/internal/adapter/outbound/redis"
	"github.com/Trisentosa/payment-module/internal/adapter/outbound/rabbitmq"
	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/application/query"
	infraDB "github.com/Trisentosa/payment-module/internal/infrastructure/db"
	"github.com/Trisentosa/payment-module/internal/infrastructure/config"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	log := logger.New(cfg.Log.Format)
	slog.SetDefault(log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := infraDB.NewPool(ctx, cfg.DB)
	if err != nil {
		log.Error("failed to connect to postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	log.Info("connected to postgres")

	// Redis
	redisClient, err := redisAdapter.NewAdapter(cfg.Redis.Addr, cfg.Redis.Password)
	if err != nil {
		log.Error("failed to connect to redis", "err", err)
		os.Exit(1)
	}
	defer func() { _ = redisClient.Close() }()
	log.Info("connected to redis")

	// RabbitMQ
	mqPublisher, err := rabbitmq.NewPublisher(cfg.RabbitMQ.URL)
	if err != nil {
		log.Error("failed to connect to rabbitmq", "err", err)
		os.Exit(1)
	}
	defer mqPublisher.Close()
	log.Info("connected to rabbitmq")

	// Outbox
	outboxWriter := postgresAdapter.NewOutboxWriter()
	outboxWorker := postgresAdapter.NewOutboxWorker(pool, mqPublisher, 50, 5*time.Second)

	// Repo
	paymentRepo := postgresAdapter.NewPaymentRepo(pool, outboxWriter)

	// Gateway
	gatewayFactory := gateway.NewFactory(map[string]ports.GatewayPort{
		"MIDTRANS": midtrans.NewAdapter(midtrans.Config{
			ServerKey: cfg.Midtrans.ServerKey,
			BaseURL:   cfg.Midtrans.BaseURL,
			Timeout:   time.Duration(cfg.Midtrans.TimeoutMs) * time.Millisecond,
		}),
	})

	// Command handlers
	createPaymentHandler := command.NewCreatePaymentHandler(paymentRepo, gatewayFactory, outboxWriter)
	processCallbackHandler := command.NewProcessCallbackHandler(paymentRepo, gatewayFactory, redisClient)
	cancelPaymentHandler := command.NewCancelPaymentHandler(paymentRepo, gatewayFactory)

	// Query handlers
	getPaymentHandler := query.NewGetPaymentHandler(paymentRepo, redisClient, redisAdapter.TTLForStatus)
	getByRefHandler := query.NewGetByRefHandler(paymentRepo, redisClient, getPaymentHandler)
	listPaymentsHandler := query.NewListPaymentsHandler(paymentRepo)

	// Idempotency
	idemService := httpMiddleware.NewIdempotencyService(redisClient, 24*time.Hour)

	// HTTP handlers
	paymentHandler := httpAdapter.NewPaymentHandler(
		createPaymentHandler,
		cancelPaymentHandler,
		idemService,
		getPaymentHandler,
		getByRefHandler,
		listPaymentsHandler,
	)
	webhookHandler := httpAdapter.NewWebhookHandler(processCallbackHandler)

	// Router
	mux := http.NewServeMux()

	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		http.ServeFile(w, r, "api/openapi.yaml")
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			logger.FromContext(r.Context()).Error("readiness check failed", "err", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "unavailable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Write
	mux.HandleFunc("POST /payments", paymentHandler.CreatePayment)
	mux.HandleFunc("DELETE /payments/{id}", paymentHandler.CancelPayment)

	// Read
	mux.HandleFunc("GET /payments", paymentHandler.ListPayments)
	mux.HandleFunc("GET /payments/{id}", paymentHandler.GetPayment)
	mux.HandleFunc("GET /payments/ref/{reference_id}", paymentHandler.GetPaymentByRef)

	// Webhooks
	mux.HandleFunc("POST /webhooks/midtrans", webhookHandler.HandleMidtrans)

	handler := httpMiddleware.RequestLogger(log)(mux)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Background workers
	go outboxWorker.Run(ctx)
	go postgresAdapter.NewExpiryJob(paymentRepo, redisClient).Run(ctx)

	go func() {
		log.Info("server starting", "port", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down server")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("server shutdown error", "err", err)
	}
}
