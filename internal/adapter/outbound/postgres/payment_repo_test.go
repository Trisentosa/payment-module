package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxAdapter "github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pgc, err := tcpostgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:16-alpine"),
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

func runMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	migrations := []string{
		`CREATE TABLE IF NOT EXISTS payments (
			id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			reference_id             VARCHAR(255) NOT NULL,
			caller_service           VARCHAR(100) NOT NULL,
			gateway_type             VARCHAR(50)  NOT NULL,
			gateway_transaction_id   VARCHAR(255),
			status                   VARCHAR(50)  NOT NULL DEFAULT 'INITIATED',
			amount                   BIGINT       NOT NULL CHECK (amount > 0),
			currency                 VARCHAR(10)  NOT NULL DEFAULT 'IDR',
			payment_method_type      VARCHAR(50),
			customer_info            JSONB        NOT NULL DEFAULT '{}',
			metadata                 JSONB                 DEFAULT '{}',
			gateway_request_payload  JSONB,
			gateway_response_payload JSONB,
			description              TEXT,
			expired_at               TIMESTAMPTZ,
			paid_at                  TIMESTAMPTZ,
			created_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			deleted_at               TIMESTAMPTZ,
			CONSTRAINT uq_payments_reference UNIQUE (reference_id, caller_service)
		)`,
		`CREATE TABLE IF NOT EXISTS payment_events (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			aggregate_id    UUID         NOT NULL,
			aggregate_type  VARCHAR(50)  NOT NULL,
			event_type      VARCHAR(100) NOT NULL,
			sequence_number INT          NOT NULL,
			payload         JSONB        NOT NULL,
			created_by      VARCHAR(100) NOT NULL DEFAULT 'system',
			created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,
	}

	for _, sql := range migrations {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("migration failed: %v", err)
		}
	}
}

func newTestPayment(referenceID string) *payment.Payment {
	m, _ := payment.NewMoney(75000, "IDR")
	p, _ := payment.New(
		referenceID, "order-service",
		m, payment.GatewayMidtrans, "BANK_TRANSFER",
		payment.CustomerInfo{ExternalID: "u1", Name: "Bob", Email: "bob@example.com"},
		"Integration test payment", nil, map[string]any{"source": "test"},
	)
	return p
}

func TestPaymentRepo_SaveAndFindByID(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool)
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
	repo := pgxAdapter.NewPaymentRepo(pool)
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
	repo := pgxAdapter.NewPaymentRepo(pool)
	ctx := context.Background()

	_, err := repo.FindByReference(ctx, "does-not-exist", "svc")
	if !apperror.IsCode(err, apperror.CodeNotFound) {
		t.Errorf("expected NOT_FOUND, got %v", err)
	}
}

func TestPaymentRepo_FindByID_NotFound(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool)
	ctx := context.Background()

	_, err := repo.FindByID(ctx, uuid.New())
	if !apperror.IsCode(err, apperror.CodeNotFound) {
		t.Errorf("expected NOT_FOUND, got %v", err)
	}
}

func TestPaymentRepo_UpdateStatus(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool)
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

func TestPaymentRepo_Save_Idempotent(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool)
	ctx := context.Background()

	refID := fmt.Sprintf("REF-%s", uuid.New())
	p1 := newTestPayment(refID)
	if err := repo.Save(ctx, p1); err != nil {
		t.Fatalf("first Save: %v", err)
	}

	p2 := newTestPayment(refID)
	// second save with same (reference_id, caller_service) — must not error
	if err := repo.Save(ctx, p2); err != nil {
		t.Fatalf("second Save (ON CONFLICT DO NOTHING): %v", err)
	}
}
