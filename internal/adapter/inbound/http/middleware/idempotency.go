package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Trisentosa/payment-module/internal/infrastructure/cache"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

// IdempotencyKey computes the deduplication key from caller + reference.
func IdempotencyKey(callerService, referenceID string) string {
	h := sha256.Sum256([]byte(callerService + ":" + referenceID))
	return fmt.Sprintf("idempotency:%x", h)
}

// RequestFingerprint hashes a request so a reused key with a different payload can be told apart.
// Pass the decoded request struct: re-marshalling it gives a stable field order.
func RequestFingerprint(req any) string {
	b, _ := json.Marshal(req)
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h)
}

type IdempotencyOutcome int

const (
	// IdempotencyNew means this request holds the key and should run.
	IdempotencyNew IdempotencyOutcome = iota
	// IdempotencyReplay means an identical request already finished; send its stored response.
	IdempotencyReplay
	// IdempotencyInFlight means an identical request is still running.
	IdempotencyInFlight
	// IdempotencyMismatch means the key was used with a different payload.
	IdempotencyMismatch
)

type IdempotencyResult struct {
	Outcome IdempotencyOutcome
	Status  int
	Body    []byte
}

const (
	stateProcessing = "processing"
	stateDone       = "done"

	// processingTTL caps how long an unfinished claim blocks retries if the process dies
	// mid-request. It must outlast the slowest request (Midtrans timeout is 30s by default).
	processingTTL = time.Minute
)

type idempotencyRecord struct {
	State       string          `json:"state"`
	RequestHash string          `json:"request_hash"`
	Status      int             `json:"status,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"`
}

type IdempotencyService struct {
	cache cache.Client
	ttl   time.Duration
}

func NewIdempotencyService(c cache.Client, ttl time.Duration) *IdempotencyService {
	return &IdempotencyService{cache: c, ttl: ttl}
}

// Begin claims key for a request with the given fingerprint, or reports why it can't.
// A non-nil error means the cache is unavailable and the outcome is unknown.
func (s *IdempotencyService) Begin(ctx context.Context, key, requestHash string) (IdempotencyResult, error) {
	claim, err := json.Marshal(idempotencyRecord{State: stateProcessing, RequestHash: requestHash})
	if err != nil {
		return IdempotencyResult{}, apperror.Internal("marshal idempotency record", err)
	}

	acquired, err := s.cache.SetNX(ctx, key, string(claim), min(processingTTL, s.ttl))
	if err != nil {
		return IdempotencyResult{}, err
	}
	if acquired {
		return IdempotencyResult{Outcome: IdempotencyNew}, nil
	}

	raw, err := s.cache.Get(ctx, key)
	if err != nil {
		if isCacheMiss(err) {
			// The key expired or was released between SETNX and GET; the caller can retry.
			return IdempotencyResult{Outcome: IdempotencyInFlight}, nil
		}
		return IdempotencyResult{}, err
	}

	var rec idempotencyRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		// Unreadable record (e.g. the pre-2026-10 "PROCESSING" marker): treat as in flight
		// until it expires rather than risk running the request twice.
		return IdempotencyResult{Outcome: IdempotencyInFlight}, nil
	}
	if rec.RequestHash != requestHash {
		return IdempotencyResult{Outcome: IdempotencyMismatch}, nil
	}
	if rec.State == stateDone {
		return IdempotencyResult{Outcome: IdempotencyReplay, Status: rec.Status, Body: rec.Body}, nil
	}
	return IdempotencyResult{Outcome: IdempotencyInFlight}, nil
}

// Complete stores the final response so identical retries replay it.
func (s *IdempotencyService) Complete(ctx context.Context, key, requestHash string, status int, body []byte) error {
	rec, err := json.Marshal(idempotencyRecord{
		State:       stateDone,
		RequestHash: requestHash,
		Status:      status,
		Body:        body,
	})
	if err != nil {
		return apperror.Internal("marshal idempotency record", err)
	}
	return s.cache.Set(ctx, key, string(rec), s.ttl)
}

// Release frees the key after a failed request, so the caller may retry with the same key.
func (s *IdempotencyService) Release(ctx context.Context, key string) error {
	return s.cache.Delete(ctx, key)
}

func isCacheMiss(err error) bool {
	return apperror.IsCode(err, apperror.CodeNotFound) || errors.Is(err, cache.ErrNotFound)
}
