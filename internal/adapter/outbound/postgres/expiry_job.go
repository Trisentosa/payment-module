package postgres

import (
	"context"
	"time"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
)

const (
	expiryLockKey  = "lock:expiry-job"
	expiryLockTTL  = 90 * time.Second
	expiryBatch    = 100
	expiryInterval = 60 * time.Second
)

type ExpiryJob struct {
	repo  payment.Repository
	cache ports.CachePort
}

func NewExpiryJob(repo payment.Repository, cache ports.CachePort) *ExpiryJob {
	return &ExpiryJob{repo: repo, cache: cache}
}

func (j *ExpiryJob) Run(ctx context.Context) {
	ticker := time.NewTicker(expiryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.RunOnce(ctx)
		}
	}
}

// RunOnce expires overdue PENDING payments, unless another replica holds the lock.
func (j *ExpiryJob) RunOnce(ctx context.Context) {
	log := logger.FromContext(ctx)

	acquired, err := j.cache.SetNX(ctx, expiryLockKey, "1", expiryLockTTL)
	if err != nil || !acquired {
		return
	}
	// Release on the way out. The TTL only covers a replica that dies mid-run.
	defer func() { _ = j.cache.Delete(ctx, expiryLockKey) }()

	expired, err := j.repo.FindExpired(ctx, expiryBatch)
	if err != nil {
		log.Error("expiry job: find expired failed", "err", err)
		return
	}

	for _, p := range expired {
		if err := j.expireOne(ctx, p); err != nil {
			log.Error("expiry job: expire failed", "payment_id", p.ID, "err", err)
		}
	}
}

func (j *ExpiryJob) expireOne(ctx context.Context, p *payment.Payment) error {
	now := time.Now().UTC()
	if p.ExpiredAt != nil && p.ExpiredAt.After(now) {
		return nil
	}

	if err := p.MarkExpired(); err != nil {
		return err
	}

	if err := j.repo.Save(ctx, p); err != nil {
		return err
	}

	ports.DropCachedPayment(ctx, j.cache, p)
	return nil
}
