package middleware

import (
	"net/http"
)

// AdminAuth returns a middleware that validates the X-Admin-Key header against the configured key.
func AdminAuth(adminKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Admin-Key") != adminKey {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"UNAUTHORIZED","message":"invalid or missing admin key"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
