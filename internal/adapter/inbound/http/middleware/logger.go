package middleware

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/infrastructure/telemetry"
)

func RequestLogger(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			span := trace.SpanFromContext(r.Context())
			l := base.With(
				"trace_id", r.Header.Get("X-Trace-Id"),
				"span_id", span.SpanContext().SpanID().String(),
				"caller_service", r.Header.Get("X-Service-Name"),
				"method", r.Method,
				"path", r.URL.Path,
			)
			ctx := logger.WithContext(r.Context(), l)

			ww := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(ww, r.WithContext(ctx))
			duration := time.Since(start)

			l.Info("request completed",
				"status", ww.status,
				"duration_ms", duration.Milliseconds(),
				"bytes", ww.bytes,
			)

			telemetry.HTTPRequestDuration.
				WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(ww.status)).
				Observe(duration.Seconds())
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += n
	return n, err
}
