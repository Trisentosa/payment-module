package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "paygate_http_request_duration_seconds",
		Help:    "HTTP request latency by method, path, and status",
		Buckets: []float64{.01, .025, .05, .1, .25, .5, .8, 1, 2.5, 5},
	}, []string{"method", "path", "status"})

	PaymentCreatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "paygate_payments_created_total",
		Help: "Total payment creation attempts by gateway and status",
	}, []string{"gateway", "status"})

	PaymentStatusTransitions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "paygate_payment_status_transitions_total",
		Help: "State machine transitions by from/to status",
	}, []string{"from", "to"})

	GatewayCallDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "paygate_gateway_call_duration_seconds",
		Help:    "Outbound gateway call latency by gateway and operation",
		Buckets: []float64{.1, .25, .5, .8, 1, 2, 5, 10},
	}, []string{"gateway", "operation"})

	GatewayCallErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "paygate_gateway_call_errors_total",
		Help: "Gateway call errors by gateway and operation",
	}, []string{"gateway", "operation", "error_type"})

	OutboxPendingGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "paygate_outbox_pending_count",
		Help: "Current number of pending outbox events",
	})

	OutboxPublishDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "paygate_outbox_publish_duration_seconds",
		Help:    "Time taken to publish a single outbox event",
		Buckets: []float64{.001, .005, .01, .05, .1, .5, 1},
	})

	CacheHitTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "paygate_cache_operations_total",
		Help: "Cache operations by result (hit/miss/error)",
	}, []string{"result"})
)
