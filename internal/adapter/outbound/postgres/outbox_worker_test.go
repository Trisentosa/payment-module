//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	pgxAdapter "github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres"
	"github.com/Trisentosa/payment-module/internal/application/ports"
)

// recordingPublisher records what it publishes; it fails the first `fail` calls and can be slowed.
type recordingPublisher struct {
	mu        sync.Mutex
	published []ports.DomainEvent
	fail      int
	delay     time.Duration
}

func (p *recordingPublisher) Publish(_ context.Context, e ports.DomainEvent) error {
	time.Sleep(p.delay)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail > 0 {
		p.fail--
		return errors.New("broker unavailable")
	}
	p.published = append(p.published, e)
	return nil
}

func (p *recordingPublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.published)
}

func insertOutbox(t *testing.T, pool *pgxpool.Pool, n int) []uuid.UUID {
	t.Helper()
	var ids []uuid.UUID
	for i := 0; i < n; i++ {
		var id uuid.UUID
		err := pool.QueryRow(context.Background(),
			`INSERT INTO outbox_events (aggregate_id, event_type, payload, created_at)
			 VALUES ($1, 'PaymentPending', '{"n":1}', NOW() - interval '10 minutes') RETURNING id`, uuid.New()).Scan(&id)
		if err != nil {
			t.Fatalf("insert outbox: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

type outboxRow struct {
	status      string
	retries     int
	scheduledIn time.Duration // scheduled_at - now
}

func readOutbox(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) outboxRow {
	t.Helper()
	var r outboxRow
	var secs float64
	err := pool.QueryRow(context.Background(),
		`SELECT status, retry_count, EXTRACT(EPOCH FROM scheduled_at - NOW()) FROM outbox_events WHERE id = $1`, id).
		Scan(&r.status, &r.retries, &secs)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	r.scheduledIn = time.Duration(secs * float64(time.Second))
	return r
}

func TestOutboxWorker_PublishesAndMarks(t *testing.T) {
	pool := setupTestDB(t)
	ids := insertOutbox(t, pool, 3)
	pub := &recordingPublisher{}

	pgxAdapter.NewOutboxWorker(pool, pub, 50, time.Second).RunOnce(context.Background())

	if pub.count() != 3 {
		t.Fatalf("published %d, want 3", pub.count())
	}
	for _, id := range ids {
		if r := readOutbox(t, pool, id); r.status != "PUBLISHED" {
			t.Errorf("%s: status %s, want PUBLISHED", id, r.status)
		}
	}
	// occurred_at must be when the event happened, not when it got published.
	if age := time.Since(pub.published[0].OccurredAt); age < 9*time.Minute {
		t.Errorf("occurred_at is %v old, want the row's creation time (10 min ago)", age.Round(time.Second))
	}
}

func TestOutboxWorker_BacksOffAndGivesUp(t *testing.T) {
	pool := setupTestDB(t)
	id := insertOutbox(t, pool, 1)[0]
	pub := &recordingPublisher{fail: 100}
	w := pgxAdapter.NewOutboxWorker(pool, pub, 50, time.Second)
	ctx := context.Background()

	w.RunOnce(ctx)
	r := readOutbox(t, pool, id)
	if r.status != "PENDING" || r.retries != 1 {
		t.Fatalf("after first failure: %+v", r)
	}
	// Regression: the first retry used to be scheduled immediately (0 * 30s).
	if r.scheduledIn < 25*time.Second {
		t.Errorf("first retry scheduled in %v, want about 30s", r.scheduledIn.Round(time.Second))
	}

	// Five retries, then give up.
	for i := 2; i <= 6; i++ {
		if _, err := pool.Exec(ctx, `UPDATE outbox_events SET scheduled_at = NOW() WHERE id = $1`, id); err != nil {
			t.Fatalf("reschedule: %v", err)
		}
		w.RunOnce(ctx)
	}
	if r := readOutbox(t, pool, id); r.status != "FAILED" || r.retries != 6 {
		t.Errorf("after 6 failures: %+v, want FAILED", r)
	}
}

// Regression (F5): FOR UPDATE SKIP LOCKED ran outside a transaction, so its locks were
// released at once and two replicas published the same rows.
func TestOutboxWorker_ConcurrentWorkersPublishEachEventOnce(t *testing.T) {
	pool := setupTestDB(t)
	insertOutbox(t, pool, 6)
	pub := &recordingPublisher{delay: 50 * time.Millisecond}
	ctx := context.Background()

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pgxAdapter.NewOutboxWorker(pool, pub, 50, time.Second).RunOnce(ctx)
		}()
	}
	wg.Wait()

	seen := map[uuid.UUID]int{}
	for _, e := range pub.published {
		seen[e.ID]++
	}
	if len(pub.published) != 6 || len(seen) != 6 {
		t.Errorf("published %d messages for %d distinct events, want 6 and 6", len(pub.published), len(seen))
	}
}
