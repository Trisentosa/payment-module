//go:build integration

package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	pgxAdapter "github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres"
	"github.com/Trisentosa/payment-module/internal/application/ports"
)

func TestAttemptRecorder_NumbersAttemptsPerPayment(t *testing.T) {
	pool := setupTestDB(t)
	rec := pgxAdapter.NewAttemptRecorder(pool)
	admin := pgxAdapter.NewAdminRepo(pool)
	ctx := context.Background()

	paymentID, refundID := uuid.New(), uuid.New()
	attempts := []ports.Attempt{
		{PaymentID: paymentID, Operation: ports.OperationCharge, Outcome: ports.AttemptUnknown,
			ErrorCode: "GATEWAY_ERROR", ErrorMessage: "i/o timeout", Duration: 30 * time.Second},
		{PaymentID: paymentID, Operation: ports.OperationStatus, Outcome: ports.AttemptSucceeded,
			GatewayTransactionID: "tx-1", GatewayResponse: map[string]any{"transaction_status": "capture"}},
		{PaymentID: paymentID, RefundID: &refundID, Operation: ports.OperationRefund, Outcome: ports.AttemptFailed,
			ErrorCode: "DECLINED", ErrorMessage: "midtrans 412"},
	}
	for _, a := range attempts {
		if err := rec.Record(ctx, a); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	rows, err := admin.GetPaymentAttempts(ctx, paymentID)
	if err != nil {
		t.Fatalf("GetPaymentAttempts: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows: %d, want 3", len(rows))
	}
	for i, row := range rows {
		if row.AttemptNumber != int32(i+1) || row.Operation != string(attempts[i].Operation) || row.Status != string(attempts[i].Outcome) {
			t.Errorf("row %d: %+v", i, row)
		}
	}
	if rows[0].DurationMs != 30000 || rows[0].ErrorMessage != "i/o timeout" {
		t.Errorf("charge row: %+v", rows[0])
	}
	if rows[2].RefundID == nil || *rows[2].RefundID != refundID {
		t.Errorf("refund row refund_id: %v", rows[2].RefundID)
	}
}

func TestAttemptRecorder_ConcurrentAttemptsGetDistinctNumbers(t *testing.T) {
	pool := setupTestDB(t)
	rec := pgxAdapter.NewAttemptRecorder(pool)
	ctx := context.Background()
	paymentID := uuid.New()

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- rec.Record(ctx, ports.Attempt{PaymentID: paymentID, Operation: ports.OperationStatus, Outcome: ports.AttemptSucceeded})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Record: %v", err)
		}
	}

	var n, distinct int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*), COUNT(DISTINCT attempt_number) FROM payment_attempts WHERE payment_id = $1`, paymentID).
		Scan(&n, &distinct); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 || distinct != 3 {
		t.Errorf("attempts=%d distinct numbers=%d, want 3 and 3", n, distinct)
	}
}

func TestPaymentRepo_ReconcilerFinders(t *testing.T) {
	pool := setupTestDB(t)
	repo := pgxAdapter.NewPaymentRepo(pool, pgxAdapter.NewOutboxWriter())
	ctx := context.Background()

	// An INITIATED payment: what an unknown charge outcome leaves behind.
	orphan := newTestPayment("ORPHAN-" + uuid.NewString())
	if err := repo.Save(ctx, orphan); err != nil {
		t.Fatalf("Save orphan: %v", err)
	}
	// A completed payment with a PENDING refund.
	withRefund := completedTestPayment(t, ctx, repo)
	if _, err := withRefund.RequestRefund("rf-1", 1000, "", "svc"); err != nil {
		t.Fatalf("RequestRefund: %v", err)
	}
	if err := repo.Save(ctx, withRefund); err != nil {
		t.Fatalf("Save refund: %v", err)
	}

	future := time.Now().Add(time.Minute)
	charges, err := repo.FindAwaitingChargeOutcome(ctx, future, 10)
	if err != nil || len(charges) != 1 || charges[0].ID != orphan.ID {
		t.Fatalf("awaiting charge outcome: %v err=%v, want only the INITIATED payment", charges, err)
	}
	refunds, err := repo.FindWithPendingRefunds(ctx, future, 10)
	if err != nil || len(refunds) != 1 || refunds[0].ID != withRefund.ID || len(refunds[0].Refunds) != 1 {
		t.Fatalf("pending refunds: %v err=%v", refunds, err)
	}

	past := time.Now().Add(-time.Hour)
	if got, _ := repo.FindAwaitingChargeOutcome(ctx, past, 10); len(got) != 0 {
		t.Errorf("cutoff ignored for charges: %d", len(got))
	}
	if got, _ := repo.FindWithPendingRefunds(ctx, past, 10); len(got) != 0 {
		t.Errorf("cutoff ignored for refunds: %d", len(got))
	}
}
