package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/infrastructure/telemetry"
)

// requestInfo carries facts learned deeper in the chain, such as the authenticated caller,
// back out to the request's summary log line.
type requestInfo struct{ caller string }

type requestInfoKey struct{}

func recordCaller(ctx context.Context, service string) {
	if info, ok := ctx.Value(requestInfoKey{}).(*requestInfo); ok {
		info.caller = service
	}
}

func RequestLogger(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			span := trace.SpanFromContext(r.Context())
			l := base.With(
				"trace_id", r.Header.Get("X-Trace-Id"),
				"span_id", span.SpanContext().SpanID().String(),
				"method", r.Method,
				"path", r.URL.Path,
			)
			info := &requestInfo{}
			ctx := context.WithValue(logger.WithContext(r.Context(), l), requestInfoKey{}, info)

			ww := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(ww, r.WithContext(ctx))
			duration := time.Since(start)

			l.Info("request completed",
				"caller_service", info.caller,
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
