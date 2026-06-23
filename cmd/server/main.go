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
	"github.com/Trisentosa/payment-module/internal/adapter/outbound/rabbitmq"
	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/infrastructure/cache"
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

	// Infrastructure stubs (real implementations added in TD-03)
	cacheClient := &cache.NoopClient{}
	publisher := &rabbitmq.NoopPublisher{}

	// Outbox
	outboxWriter := postgresAdapter.NewOutboxWriter()
	outboxWorker := postgresAdapter.NewOutboxWorker(pool, publisher, 50, 5*time.Second)

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
	processCallbackHandler := command.NewProcessCallbackHandler(paymentRepo, gatewayFactory, cacheClient)
	cancelPaymentHandler := command.NewCancelPaymentHandler(paymentRepo, gatewayFactory)

	// Idempotency
	idemService := httpMiddleware.NewIdempotencyService(cacheClient, 24*time.Hour)

	// HTTP handlers
	paymentHandler := httpAdapter.NewPaymentHandler(createPaymentHandler, cancelPaymentHandler, idemService)
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

	mux.HandleFunc("POST /payments", paymentHandler.CreatePayment)
	mux.HandleFunc("DELETE /payments/{id}", paymentHandler.CancelPayment)
	mux.HandleFunc("POST /webhooks/midtrans", webhookHandler.HandleMidtrans)

	handler := httpMiddleware.RequestLogger(log)(mux)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start outbox worker
	go outboxWorker.Run(ctx)

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
