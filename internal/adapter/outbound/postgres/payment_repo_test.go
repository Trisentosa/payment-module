//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	pgxAdapter "github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupTestDB returns a migrated, empty database. It starts a Postgres container, unless
// PAYGATE_TEST_DATABASE_URL points at an existing database, which is then wiped per test
// (for machines without Docker). Never point it at a database you care about.
func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	if url := os.Getenv("PAYGATE_TEST_DATABASE_URL"); url != "" {
		pool, err := pgxpool.New(ctx, url)
		if err != nil {
			t.Fatalf("pgxpool.New: %v", err)
		}
		t.Cleanup(pool.Close)
		if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatalf("reset schema: %v", err)
		}
		runMigrations(t, pool)
		return pool
	}

	pgc, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("paygate_test"),
		tcpostgres.WithUsername("paygate_user"),
		tcpostgres.WithPassword("secret"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = pgc.Terminate(ctx) })

	connStr, err := pgc.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	runMigrations(t, pool)
	return pool
}

// runMigrations applies the real migration files in order, so the test schema can't
// drift from production.
func runMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	files, err := filepath.Glob("../../../../migrations/*.up.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("find migrations: %v (found %d)", err, len(files))
	}
	sort.Strings(files)
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(f), err)
		}
	}
}

func newTestPayment(referenceID string) *payment.Payment {
	m, _ := payment.NewMoney(75000, "IDR")
	p, _ := payment.New(
		referenceID, "order-service",
		m, payment.GatewayMidtrans, payment.MethodVirtualAccount,
		payment.CustomerInfo{ExternalID: "u1", Name: "Bob", Email: "bob@example.com"},
		"Integration test payment", nil, map[string]any{"source": "test"},
	)
	return p
}

func TestPaymentRepo_SaveAndFindByID(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool, pgxAdapter.NewOutboxWriter())
	ctx := context.Background()

	p := newTestPayment(fmt.Sprintf("REF-%s", uuid.New()))
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	found, err := repo.FindByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}

	if found.ID != p.ID {
		t.Errorf("ID mismatch: got %s, want %s", found.ID, p.ID)
	}
	if found.Status != payment.StatusInitiated {
		t.Errorf("Status: got %s, want %s", found.Status, payment.StatusInitiated)
	}
	if found.Amount.Amount != 75000 {
		t.Errorf("Amount: got %d, want 75000", found.Amount.Amount)
	}
	if found.Amount.Currency != "IDR" {
		t.Errorf("Currency: got %s, want IDR", found.Amount.Currency)
	}
	if found.CustomerInfo.Name != "Bob" {
		t.Errorf("CustomerInfo.Name: got %s, want Bob", found.CustomerInfo.Name)
	}
}

func TestPaymentRepo_FindByReference(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool, pgxAdapter.NewOutboxWriter())
	ctx := context.Background()

	refID := fmt.Sprintf("REF-%s", uuid.New())
	p := newTestPayment(refID)
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	found, err := repo.FindByReference(ctx, refID, "order-service")
	if err != nil {
		t.Fatalf("FindByReference: %v", err)
	}
	if found.ID != p.ID {
		t.Errorf("ID mismatch")
	}
}

func TestPaymentRepo_FindByReference_NotFound(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool, pgxAdapter.NewOutboxWriter())
	ctx := context.Background()

	_, err := repo.FindByReference(ctx, "does-not-exist", "svc")
	if !apperror.IsCode(err, apperror.CodeNotFound) {
		t.Errorf("expected NOT_FOUND, got %v", err)
	}
}

func TestPaymentRepo_FindByID_NotFound(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool, pgxAdapter.NewOutboxWriter())
	ctx := context.Background()

	_, err := repo.FindByID(ctx, uuid.New())
	if !apperror.IsCode(err, apperror.CodeNotFound) {
		t.Errorf("expected NOT_FOUND, got %v", err)
	}
}

func TestPaymentRepo_UpdateStatus(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool, pgxAdapter.NewOutboxWriter())
	ctx := context.Background()

	p := newTestPayment(fmt.Sprintf("REF-%s", uuid.New()))
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := repo.UpdateStatus(ctx, p.ID, payment.StatusFailed, nil); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	found, _ := repo.FindByID(ctx, p.ID)
	if found.Status != payment.StatusFailed {
		t.Errorf("Status after update: got %s, want FAILED", found.Status)
	}
}

func TestPaymentRepo_Save_DuplicateReference(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool, pgxAdapter.NewOutboxWriter())
	ctx := context.Background()

	refID := fmt.Sprintf("REF-%s", uuid.New())
	p1 := newTestPayment(refID)
	if err := repo.Save(ctx, p1); err != nil {
		t.Fatalf("first Save: %v", err)
	}

	p2 := newTestPayment(refID)
	err := repo.Save(ctx, p2)
	if !apperror.IsCode(err, apperror.CodeAlreadyExists) {
		t.Fatalf("second Save with same reference: expected ALREADY_EXISTS, got %v", err)
	}

	// The rejected payment must leave no trace: no events, no outbox rows.
	var events, outbox int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM payment_events WHERE aggregate_id = $1`, p2.ID).Scan(&events); err != nil {
		t.Fatalf("count payment_events: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = $1`, p2.ID).Scan(&outbox); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	if events != 0 || outbox != 0 {
		t.Errorf("phantom rows for rejected payment: payment_events=%d outbox_events=%d", events, outbox)
	}
}

func completedTestPayment(t *testing.T, ctx context.Context, repo *pgxAdapter.PaymentRepo) *payment.Payment {
	t.Helper()
	p := newTestPayment(fmt.Sprintf("REF-%s", uuid.New()))
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save new: %v", err)
	}
	if err := p.MarkPending("gw-"+p.ID.String(), nil); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}
	if err := p.MarkCompleted(time.Now()); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save completed: %v", err)
	}
	return p
}

func TestPaymentRepo_RefundsRoundTrip(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool, pgxAdapter.NewOutboxWriter())
	ctx := context.Background()

	p := completedTestPayment(t, ctx, repo)
	r, err := p.RequestRefund("rf-1", 25000, "damaged", "order-service")
	if err != nil {
		t.Fatalf("RequestRefund: %v", err)
	}
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save pending refund: %v", err)
	}
	if err := p.CompleteRefund(r.ID, "7"); err != nil {
		t.Fatalf("CompleteRefund: %v", err)
	}
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save completed refund: %v", err)
	}

	found, err := repo.FindByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if found.Status != payment.StatusPartiallyRefunded || len(found.Refunds) != 1 {
		t.Fatalf("found: status=%s refunds=%d", found.Status, len(found.Refunds))
	}
	got := found.Refunds[0]
	if got.ID != r.ID || got.Status != payment.RefundStatusSucceeded || got.GatewayRefundID != "7" ||
		got.Amount != 25000 || got.RequestedBy != "order-service" || got.CompletedAt == nil {
		t.Errorf("refund round trip: %+v", got)
	}

	var outbox int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = $1 AND event_type LIKE 'Refund%'`, p.ID).Scan(&outbox); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outbox != 2 {
		t.Errorf("refund outbox rows: %d, want 2 (requested, completed)", outbox)
	}
}

func TestPaymentRepo_StaleSaveConflicts(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool, pgxAdapter.NewOutboxWriter())
	ctx := context.Background()

	p := completedTestPayment(t, ctx, repo) // 75000 IDR
	a, _ := repo.FindByID(ctx, p.ID)
	b, _ := repo.FindByID(ctx, p.ID)

	if _, err := a.RequestRefund("rf-a", 50000, "", "svc"); err != nil {
		t.Fatalf("RequestRefund a: %v", err)
	}
	if err := repo.Save(ctx, a); err != nil {
		t.Fatalf("Save a: %v", err)
	}

	// b was loaded before a's refund, so it still sees 75000 as refundable. Together the two
	// refunds would exceed what was paid; the version check must stop b.
	if _, err := b.RequestRefund("rf-b", 40000, "", "svc"); err != nil {
		t.Fatalf("RequestRefund b: %v", err)
	}
	if err := repo.Save(ctx, b); !apperror.IsCode(err, apperror.CodeConflict) {
		t.Fatalf("stale save: expected CONFLICT, got %v", err)
	}

	found, err := repo.FindByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if len(found.Refunds) != 1 || found.RefundableAmount() != 25000 {
		t.Errorf("after conflict: refunds=%d refundable=%d, want only a's refund (25000 left)",
			len(found.Refunds), found.RefundableAmount())
	}
}

func TestPaymentRepo_ListPaginated(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool, pgxAdapter.NewOutboxWriter())
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := repo.Save(ctx, newTestPayment(fmt.Sprintf("LIST-%d-%s", i, uuid.New()))); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, err := repo.ListPaginated(ctx, payment.ListFilter{CallerService: "order-service", ReferenceID: "LIST-", Limit: 2})
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d payments, want 2 (limit)", len(got))
	}
	if got[0].Version != 1 || got[0].CreatedAt.Before(got[1].CreatedAt) {
		t.Errorf("first row: version=%d, order newest-first? %v >= %v", got[0].Version, got[0].CreatedAt, got[1].CreatedAt)
	}
}

// Regression (F8): sequence numbers restarted at 1 on every save, so a payment's event
// history couldn't be put back in order.
func TestPaymentRepo_EventSequenceContinuesAcrossSaves(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool, pgxAdapter.NewOutboxWriter())
	ctx := context.Background()

	p := completedTestPayment(t, ctx, repo) // saves: [Initiated], then [Pending, Completed]
	if _, err := p.RequestRefund("rf-1", 1000, "", "svc"); err != nil {
		t.Fatalf("RequestRefund: %v", err)
	}
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rows, err := pool.Query(ctx, `SELECT event_type, sequence_number FROM payment_events WHERE aggregate_id = $1 ORDER BY sequence_number`, p.ID)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var typ string
		var seq int
		if err := rows.Scan(&typ, &seq); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, fmt.Sprintf("%d:%s", seq, typ))
	}
	want := "[1:PaymentInitiated 2:PaymentPending 3:PaymentCompleted 4:RefundRequested]"
	if fmt.Sprint(got) != want {
		t.Errorf("events: %v, want %s", got, want)
	}
}
