package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/pkg/apikey"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type callerKey struct{}

// CallerService returns the authenticated calling service. It is empty for requests that
// didn't pass through CallerAuth.
func CallerService(ctx context.Context) string {
	s, _ := ctx.Value(callerKey{}).(string)
	return s
}

// CallerAuth identifies the calling service from its API key (Authorization: Bearer <key>).
// keyHashes maps the SHA-256 hex of each valid key to its service name. Comparing hashes
// means the lookup's timing reveals nothing useful about any real key.
//
// X-Service-Name is no longer trusted. If a caller still sends it, it must name the key's
// own service; a mismatch is rejected so a misconfigured caller fails loudly.
func CallerAuth(keyHashes map[string]string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, ok := bearerToken(r)
			service := ""
			if ok {
				service = keyHashes[apikey.Hash(key)]
			}
			if service == "" {
				WriteError(w, apperror.Unauthorized("missing or invalid API key; send Authorization: Bearer <key>"))
				return
			}
			if claimed := r.Header.Get("X-Service-Name"); claimed != "" && claimed != service {
				WriteError(w, apperror.Forbidden("X-Service-Name does not match the service this API key belongs to"))
				return
			}

			ctx := context.WithValue(r.Context(), callerKey{}, service)
			ctx = logger.WithContext(ctx, logger.FromContext(ctx).With("caller_service", service))
			recordCaller(ctx, service)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
