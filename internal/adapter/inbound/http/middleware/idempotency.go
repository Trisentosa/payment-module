package middleware

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/Trisentosa/payment-module/internal/infrastructure/cache"
	"github.com/google/uuid"
)

// IdempotencyKey computes the deduplication key from caller + reference.
func IdempotencyKey(callerService, referenceID string) string {
	h := sha256.Sum256([]byte(callerService + ":" + referenceID))
	return fmt.Sprintf("idempotency:%x", h)
}

type IdempotencyService struct {
	cache cache.Client
	ttl   time.Duration
}

func NewIdempotencyService(c cache.Client, ttl time.Duration) *IdempotencyService {
	return &IdempotencyService{cache: c, ttl: ttl}
}

// CheckOrAcquire returns (cached response, true) if duplicate, or (nil, false) if new.
func (s *IdempotencyService) CheckOrAcquire(ctx context.Context, key string) ([]byte, bool, error) {
	existing, err := s.cache.Get(ctx, key)
	if err == nil && existing != "" && existing != "PROCESSING" {
		return []byte(existing), true, nil
	}

	ok, err := s.cache.SetNX(ctx, key, "PROCESSING", s.ttl)
	if err != nil {
		return nil, false, nil
	}
	if !ok {
		return nil, true, nil
	}
	return nil, false, nil
}

// StoreResponse saves the final response payload to the cache.
func (s *IdempotencyService) StoreResponse(_ context.Context, _ string, _ uuid.UUID, _ int, _ []byte) error {
	// Redis store deferred to TD-03; DB write can be added here as well.
	return nil
}
