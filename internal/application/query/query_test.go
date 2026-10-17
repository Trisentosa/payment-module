package query_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/application/query"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/Trisentosa/payment-module/internal/testsupport/fake"
)

func ttl(payment.Status) time.Duration { return time.Minute }

func newPayment(t *testing.T, ref, caller string, createdAt time.Time) *payment.Payment {
	t.Helper()
	m, _ := payment.NewMoney(10000, "IDR")
	p, err := payment.New(ref, caller, m, payment.GatewayMidtrans, payment.MethodQRIS, payment.CustomerInfo{Name: "Ann"}, "", nil, nil)
	if err != nil {
		t.Fatalf("payment.New: %v", err)
	}
	p.CreatedAt = createdAt
	return p
}

func TestGetPayment_CachesAndScopesToOwner(t *testing.T) {
	ctx := context.Background()
	p := newPayment(t, "REF-1", "order-service", time.Now())
	repo, cache := fake.NewPaymentRepo(p), fake.NewCache()
	h := query.NewGetPaymentHandler(repo, cache, ttl)

	dto, err := h.Handle(ctx, query.GetPaymentQuery{ID: p.ID, CallerService: "order-service"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if dto.ID != p.ID.String() || dto.ReferenceID != "REF-1" {
		t.Errorf("dto: %+v", dto)
	}
	if !cache.Has(ports.PaymentCacheKey(p.ID.String())) {
		t.Error("view not cached after a miss")
	}

	// Served from cache: the cached copy wins over the store.
	_ = cache.Set(ctx, ports.PaymentCacheKey(p.ID.String()), mustJSON(t, query.PaymentDTO{ID: p.ID.String(), CallerService: "order-service", Status: "CACHED"}), 0)
	if dto, _ := h.Handle(ctx, query.GetPaymentQuery{ID: p.ID, CallerService: "order-service"}); dto.Status != "CACHED" {
		t.Errorf("status: got %s, want the cached view", dto.Status)
	}

	// Ownership holds for cached views too.
	if _, err := h.Handle(ctx, query.GetPaymentQuery{ID: p.ID, CallerService: "billing-service"}); !apperror.IsCode(err, apperror.CodeNotFound) {
		t.Errorf("other caller: expected NOT_FOUND, got %v", err)
	}
}

func TestGetByRef_ScopedByCallerAndCached(t *testing.T) {
	ctx := context.Background()
	p := newPayment(t, "REF-2", "order-service", time.Now())
	repo, cache := fake.NewPaymentRepo(p), fake.NewCache()
	h := query.NewGetByRefHandler(repo, cache, query.NewGetPaymentHandler(repo, cache, ttl))

	if _, err := h.Handle(ctx, query.GetByRefQuery{ReferenceID: "REF-2", CallerService: "order-service"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !cache.Has(ports.ReferenceCacheKey("order-service", "REF-2")) {
		t.Error("view not cached under the caller's reference key")
	}
	if _, err := h.Handle(ctx, query.GetByRefQuery{ReferenceID: "REF-2", CallerService: "billing-service"}); !apperror.IsCode(err, apperror.CodeNotFound) {
		t.Errorf("same reference, other caller: expected NOT_FOUND, got %v", err)
	}
	for name, q := range map[string]query.GetByRefQuery{
		"no reference": {CallerService: "order-service"},
		"no caller":    {ReferenceID: "REF-2"},
	} {
		if _, err := h.Handle(ctx, q); !apperror.IsCode(err, apperror.CodeInvalidInput) {
			t.Errorf("%s: expected INVALID_INPUT, got %v", name, err)
		}
	}
}

func TestListPayments_PagesWithCursor(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	var seed []*payment.Payment
	for i := 0; i < 5; i++ {
		seed = append(seed, newPayment(t, fmt.Sprintf("REF-%d", i), "order-service", base.Add(time.Duration(i)*time.Minute)))
	}
	seed = append(seed, newPayment(t, "OTHER", "billing-service", base.Add(time.Hour)))
	h := query.NewListPaymentsHandler(fake.NewPaymentRepo(seed...))

	page1, err := h.Handle(ctx, query.ListPaymentsQuery{CallerService: "order-service", Limit: 3})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if got := refs(page1); fmt.Sprint(got) != "[REF-4 REF-3 REF-2]" || page1.NextCursor == "" {
		t.Fatalf("page 1: %v next=%v", got, page1.NextCursor)
	}
	page2, err := h.Handle(ctx, query.ListPaymentsQuery{CallerService: "order-service", Limit: 3, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if got := refs(page2); fmt.Sprint(got) != "[REF-1 REF-0]" || page2.NextCursor != "" {
		t.Errorf("page 2: %v next=%v, want the last two and no cursor", got, page2.NextCursor)
	}
}

func TestListPayments_PagesThroughEqualTimestamps(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	var seed []*payment.Payment
	for i := 0; i < 5; i++ {
		seed = append(seed, newPayment(t, fmt.Sprintf("SAME-%d", i), "order-service", at))
	}
	h := query.NewListPaymentsHandler(fake.NewPaymentRepo(seed...))

	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		res, err := h.Handle(ctx, query.ListPaymentsQuery{CallerService: "order-service", Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, ref := range refs(res) {
			if seen[ref] {
				t.Errorf("%s listed twice", ref)
			}
			seen[ref] = true
		}
		if cursor = res.NextCursor; cursor == "" {
			break
		}
	}
	if len(seen) != 5 {
		t.Errorf("listed %d of 5 payments created at the same instant", len(seen))
	}
}

func TestListPayments_RejectsTamperedCursor(t *testing.T) {
	h := query.NewListPaymentsHandler(fake.NewPaymentRepo())
	for _, cursor := range []string{"not-base64!", "MjAyNi0xMC0xNVQwOTowMDowMFo"} { // the second has no id
		_, err := h.Handle(context.Background(), query.ListPaymentsQuery{CallerService: "svc", Cursor: cursor})
		if !apperror.IsCode(err, apperror.CodeInvalidInput) {
			t.Errorf("cursor %q: expected INVALID_INPUT, got %v", cursor, err)
		}
	}
}

func TestListPayments_Limits(t *testing.T) {
	repo := fake.NewPaymentRepo()
	h := query.NewListPaymentsHandler(repo)
	for requested, want := range map[int]int{0: 50, 10: 10, 100: 100, 500: 100} {
		if _, err := h.Handle(context.Background(), query.ListPaymentsQuery{CallerService: "svc", Limit: requested}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		// The handler asks for one extra row to detect a next page.
		if got := repo.LastListFilter().Limit; got != want+1 {
			t.Errorf("limit %d: repo asked for %d, want %d", requested, got, want+1)
		}
	}
	if _, err := h.Handle(context.Background(), query.ListPaymentsQuery{}); !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Errorf("no caller: expected INVALID_INPUT, got %v", err)
	}
}

func TestListRefunds_OwnerOnly(t *testing.T) {
	p := newPayment(t, "REF-R", "order-service", time.Now())
	h := query.NewListRefundsHandler(fake.NewPaymentRepo(p))

	if _, err := h.Handle(context.Background(), query.ListRefundsQuery{PaymentID: p.ID, CallerService: "order-service"}); err != nil {
		t.Fatalf("owner: %v", err)
	}
	if _, err := h.Handle(context.Background(), query.ListRefundsQuery{PaymentID: p.ID, CallerService: "billing-service"}); !apperror.IsCode(err, apperror.CodeNotFound) {
		t.Errorf("other caller: expected NOT_FOUND, got %v", err)
	}
}

func refs(r *query.ListPaymentsResult) []string {
	out := make([]string, 0, len(r.Payments))
	for _, p := range r.Payments {
		out = append(out, p.ReferenceID)
	}
	return out
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
