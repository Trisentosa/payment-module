package middleware_test

import (
	"context"
	"testing"
	"time"

	"github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/testsupport/fake"
)

func TestIdempotency_CompleteThenReplay(t *testing.T) {
	ctx := context.Background()
	svc := middleware.NewIdempotencyService(fake.NewCache(), time.Hour)

	first, err := svc.Begin(ctx, "k", "hash-a")
	if err != nil || first.Outcome != middleware.IdempotencyNew {
		t.Fatalf("first Begin: outcome=%v err=%v, want New", first.Outcome, err)
	}
	if err := svc.Complete(ctx, "k", "hash-a", 201, []byte(`{"payment_id":"p1"}`)); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	again, err := svc.Begin(ctx, "k", "hash-a")
	if err != nil {
		t.Fatalf("second Begin: %v", err)
	}
	if again.Outcome != middleware.IdempotencyReplay {
		t.Fatalf("outcome: got %v, want Replay", again.Outcome)
	}
	if again.Status != 201 || string(again.Body) != `{"payment_id":"p1"}` {
		t.Errorf("replayed status=%d body=%s", again.Status, again.Body)
	}
}

func TestIdempotency_InFlightWhileProcessing(t *testing.T) {
	ctx := context.Background()
	svc := middleware.NewIdempotencyService(fake.NewCache(), time.Hour)

	if _, err := svc.Begin(ctx, "k", "hash-a"); err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	res, err := svc.Begin(ctx, "k", "hash-a")
	if err != nil || res.Outcome != middleware.IdempotencyInFlight {
		t.Fatalf("outcome=%v err=%v, want InFlight", res.Outcome, err)
	}
}

func TestIdempotency_DifferentPayloadIsMismatch(t *testing.T) {
	ctx := context.Background()
	svc := middleware.NewIdempotencyService(fake.NewCache(), time.Hour)

	if _, err := svc.Begin(ctx, "k", "hash-a"); err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	res, err := svc.Begin(ctx, "k", "hash-b")
	if err != nil || res.Outcome != middleware.IdempotencyMismatch {
		t.Fatalf("outcome=%v err=%v, want Mismatch", res.Outcome, err)
	}
}

func TestIdempotency_ReleaseAllowsRetry(t *testing.T) {
	ctx := context.Background()
	svc := middleware.NewIdempotencyService(fake.NewCache(), time.Hour)

	if _, err := svc.Begin(ctx, "k", "hash-a"); err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	if err := svc.Release(ctx, "k"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	res, err := svc.Begin(ctx, "k", "hash-b")
	if err != nil || res.Outcome != middleware.IdempotencyNew {
		t.Fatalf("outcome=%v err=%v, want New", res.Outcome, err)
	}
}

func TestIdempotency_LegacyMarkerTreatedAsInFlight(t *testing.T) {
	ctx := context.Background()
	c := fake.NewCache()
	_ = c.Set(ctx, "k", "PROCESSING", time.Hour) // value written by the pre-fix code
	svc := middleware.NewIdempotencyService(c, time.Hour)

	res, err := svc.Begin(ctx, "k", "hash-a")
	if err != nil || res.Outcome != middleware.IdempotencyInFlight {
		t.Fatalf("outcome=%v err=%v, want InFlight", res.Outcome, err)
	}
}
