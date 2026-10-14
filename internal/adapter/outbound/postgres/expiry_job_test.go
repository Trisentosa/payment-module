package postgres_test

import (
	"context"
	"testing"
	"time"

	pgxAdapter "github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/testsupport/fake"
)

func pendingUntil(t *testing.T, ref string, expiresAt time.Time) *payment.Payment {
	t.Helper()
	m, _ := payment.NewMoney(10000, "IDR")
	p, err := payment.New(ref, "order-service", m, payment.GatewayMidtrans, payment.MethodQRIS, payment.CustomerInfo{}, "", &expiresAt, nil)
	if err != nil {
		t.Fatalf("payment.New: %v", err)
	}
	if err := p.MarkPending("tx-"+ref, nil); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}
	return p
}

func TestExpiryJob_ExpiresOverduePendingPayments(t *testing.T) {
	ctx := context.Background()
	overdue := pendingUntil(t, "OVERDUE", time.Now().Add(-time.Minute))
	current := pendingUntil(t, "CURRENT", time.Now().Add(time.Hour))
	repo, cache := fake.NewPaymentRepo(overdue, current), fake.NewCache()
	_ = cache.Set(ctx, ports.PaymentCacheKey(overdue.ID.String()), "stale", 0)

	pgxAdapter.NewExpiryJob(repo, cache).RunOnce(ctx)

	if got, _ := repo.FindByID(ctx, overdue.ID); got.Status != payment.StatusExpired {
		t.Errorf("overdue: got %s, want EXPIRED", got.Status)
	}
	if got, _ := repo.FindByID(ctx, current.ID); got.Status != payment.StatusPending {
		t.Errorf("not yet due: got %s, want PENDING", got.Status)
	}
	if cache.Has(ports.PaymentCacheKey(overdue.ID.String())) {
		t.Error("cached view of the expired payment not dropped")
	}
}

// Regression: the job never released its lock, so with a 90s lock and a 60s interval
// every other run was skipped.
func TestExpiryJob_ReleasesLockSoTheNextRunProceeds(t *testing.T) {
	ctx := context.Background()
	repo, cache := fake.NewPaymentRepo(), fake.NewCache()
	job := pgxAdapter.NewExpiryJob(repo, cache)

	job.RunOnce(ctx)
	late := pendingUntil(t, "LATE", time.Now().Add(-time.Minute))
	if err := repo.Save(ctx, late); err != nil {
		t.Fatalf("Save: %v", err)
	}
	job.RunOnce(ctx)

	if got, _ := repo.FindByID(ctx, late.ID); got.Status != payment.StatusExpired {
		t.Errorf("second run: got %s, want EXPIRED (was the lock still held?)", got.Status)
	}
}

func TestExpiryJob_SkipsWhileAnotherReplicaHoldsTheLock(t *testing.T) {
	ctx := context.Background()
	overdue := pendingUntil(t, "OVERDUE", time.Now().Add(-time.Minute))
	repo, cache := fake.NewPaymentRepo(overdue), fake.NewCache()
	_, _ = cache.SetNX(ctx, "lock:expiry-job", "other-replica", time.Minute)

	pgxAdapter.NewExpiryJob(repo, cache).RunOnce(ctx)

	if got, _ := repo.FindByID(ctx, overdue.ID); got.Status != payment.StatusPending {
		t.Errorf("status: got %s; another replica holds the lock, so nothing should change", got.Status)
	}
}
