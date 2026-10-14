package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"

	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

// AdminAuth requires the X-Admin-Key header to equal adminKey. An empty adminKey rejects
// every request; config already refuses to start without one, this is a second guard.
func AdminAuth(adminKey string) func(http.Handler) http.Handler {
	want := sha256.Sum256([]byte(adminKey))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Comparing fixed-length hashes in constant time leaks neither the key nor its length.
			got := sha256.Sum256([]byte(r.Header.Get("X-Admin-Key")))
			if adminKey == "" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
				WriteError(w, apperror.Unauthorized("invalid or missing admin key"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
