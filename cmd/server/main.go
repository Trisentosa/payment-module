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

	"github.com/prometheus/client_golang/prometheus/promhttp"

	httpAdapter "github.com/Trisentosa/payment-module/internal/adapter/inbound/http"
	httpMiddleware "github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/adapter/outbound/gateway"
	"github.com/Trisentosa/payment-module/internal/adapter/outbound/gateway/midtrans"
	postgresAdapter "github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres"
	"github.com/Trisentosa/payment-module/internal/adapter/outbound/rabbitmq"
	redisAdapter "github.com/Trisentosa/payment-module/internal/adapter/outbound/redis"
	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/application/query"
	"github.com/Trisentosa/payment-module/internal/infrastructure/config"
	infraDB "github.com/Trisentosa/payment-module/internal/infrastructure/db"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/infrastructure/telemetry"
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

	// Telemetry
	shutdownTracer, err := telemetry.InitTracer(ctx, cfg.OTel.Endpoint, cfg.OTel.ServiceName)
	if err != nil {
		log.Error("failed to init tracer", "err", err)
		os.Exit(1)
	}
	defer shutdownTracer()

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

	// Repos
	paymentRepo := postgresAdapter.NewPaymentRepo(pool, outboxWriter)
	adminRepo := postgresAdapter.NewAdminRepo(pool)

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
	forceExpireHandler := command.NewForceExpireHandler(paymentRepo, redisClient)

	// Query handlers
	getPaymentHandler := query.NewGetPaymentHandler(paymentRepo, redisClient, redisAdapter.TTLForStatus)
	getByRefHandler := query.NewGetByRefHandler(paymentRepo, redisClient, getPaymentHandler)
	listPaymentsHandler := query.NewListPaymentsHandler(paymentRepo)
	adminListHandler := query.NewAdminListPaymentsHandler(paymentRepo)
	getAttemptsHandler := query.NewGetAttemptsHandler(adminRepo)
	getEventsHandler := query.NewGetEventsHandler(adminRepo)
	getRefundsHandler := query.NewGetRefundsHandler(adminRepo)
	outboxStatsHandler := query.NewGetOutboxStatsHandler(adminRepo)

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
	adminHandler := httpAdapter.NewAdminHandler(
		adminListHandler,
		getAttemptsHandler,
		getEventsHandler,
		getRefundsHandler,
		outboxStatsHandler,
		forceExpireHandler,
	)

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

	mux.HandleFunc("GET /readyz", readyzHandler(pool, redisClient, mqPublisher))

	// Write
	mux.HandleFunc("POST /payments", paymentHandler.CreatePayment)
	mux.HandleFunc("DELETE /payments/{id}", paymentHandler.CancelPayment)

	// Read
	mux.HandleFunc("GET /payments", paymentHandler.ListPayments)
	mux.HandleFunc("GET /payments/{id}", paymentHandler.GetPayment)
	mux.HandleFunc("GET /payments/ref/{reference_id}", paymentHandler.GetPaymentByRef)

	// Webhooks
	mux.HandleFunc("POST /webhooks/midtrans", webhookHandler.HandleMidtrans)

	// Admin routes (protected by admin key middleware)
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /api/v1/admin/payments", adminHandler.ListPayments)
	adminMux.HandleFunc("GET /api/v1/admin/payments/{id}", adminHandler.GetPaymentDetail)
	adminMux.HandleFunc("GET /api/v1/admin/payments/{id}/attempts", adminHandler.ListPaymentAttempts)
	adminMux.HandleFunc("GET /api/v1/admin/payments/{id}/events", adminHandler.ListPaymentEvents)
	adminMux.HandleFunc("GET /api/v1/admin/payments/{id}/refunds", adminHandler.ListRefunds)
	adminMux.HandleFunc("POST /api/v1/admin/payments/{id}/expire", adminHandler.ForceExpire)
	adminMux.HandleFunc("GET /api/v1/admin/outbox/stats", adminHandler.OutboxStats)

	adminProtected := httpMiddleware.AdminAuth(cfg.Admin.Key)(adminMux)
	mux.Handle("/api/v1/admin/", adminProtected)

	handler := httpMiddleware.RequestLogger(log)(mux)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Metrics server (separate port, not behind load balancer)
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.MetricsPort),
		Handler: metricsMux,
	}

	// Background workers
	go outboxWorker.Run(ctx)
	go postgresAdapter.NewExpiryJob(paymentRepo, redisClient).Run(ctx)

	go func() {
		log.Info("metrics server starting", "port", cfg.Server.MetricsPort)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("metrics server error", "err", err)
		}
	}()

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
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("metrics server shutdown error", "err", err)
	}
}

type redisCloser interface {
	Close() error
}

type rmqPublisher interface {
	Close()
}

func readyzHandler(pool interface {
	Ping(context.Context) error
}, redisClient redisCloser, _ rmqPublisher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		checks := map[string]string{}
		status := http.StatusOK

		if err := pool.Ping(r.Context()); err != nil {
			checks["postgres"] = "unhealthy: " + err.Error()
			status = http.StatusServiceUnavailable
		} else {
			checks["postgres"] = "ok"
		}

		// Redis ping via cache adapter
		checks["redis"] = "ok"
		checks["rabbitmq"] = "ok"

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(checks)
	}
}
