package ports

import (
	"context"
	"time"

	"github.com/Trisentosa/payment-module/internal/domain/payment"
)

type CachePort interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
	SetNX(ctx context.Context, key string, value string, ttl time.Duration) (bool, error)
	Delete(ctx context.Context, key string) error
}

// PaymentCacheKey and ReferenceCacheKey name the cached payment views that queries write.
// Anything that changes a payment must drop both.
func PaymentCacheKey(paymentID string) string { return "payment:" + paymentID }

func ReferenceCacheKey(callerService, referenceID string) string {
	return "reference:" + callerService + ":" + referenceID
}

// DropCachedPayment deletes both cached views of p. Errors are ignored: the entries expire
// on their own, and a cache outage must not fail the write that just succeeded.
func DropCachedPayment(ctx context.Context, c CachePort, p *payment.Payment) {
	_ = c.Delete(ctx, PaymentCacheKey(p.ID.String()))
	_ = c.Delete(ctx, ReferenceCacheKey(p.CallerService, p.ReferenceID))
}
