package middleware

import (
	"log/slog"
	"net/http"

	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
)

func RequestLogger(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			l := base.With(
				"trace_id", r.Header.Get("X-Trace-Id"),
				"caller_service", r.Header.Get("X-Service-Name"),
				"method", r.Method,
				"path", r.URL.Path,
			)
			ctx := logger.WithContext(r.Context(), l)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
