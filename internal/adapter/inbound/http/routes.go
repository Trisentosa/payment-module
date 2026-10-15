package http

import "net/http"

// CallerRoutes is the routing of the caller API (/payments...). cmd/server and the tests
// both build it here, so a test can't pass against routes the server doesn't have.
func CallerRoutes(p *PaymentHandler, rf *RefundHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /payments", p.CreatePayment)
	mux.HandleFunc("GET /payments", p.ListPayments)
	mux.HandleFunc("GET /payments/{id}", p.GetPayment)
	mux.HandleFunc("DELETE /payments/{id}", p.CancelPayment)
	mux.HandleFunc("POST /payments/{id}/refunds", rf.CreateRefund)
	// ServeMux refuses GET /payments/ref/{reference_id} next to GET /payments/{id}/refunds,
	// since /payments/ref/refunds matches both, and panics at startup. One route serves both.
	mux.HandleFunc("GET /payments/{first}/{second}", func(w http.ResponseWriter, r *http.Request) {
		first, second := r.PathValue("first"), r.PathValue("second")
		switch {
		case first == "ref":
			r.SetPathValue("reference_id", second)
			p.GetPaymentByRef(w, r)
		case second == "refunds":
			r.SetPathValue("id", first)
			rf.ListRefunds(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

// AdminRoutes is the routing of the admin API (/api/v1/admin/...).
func AdminRoutes(a *AdminHandler, rf *RefundHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/admin/payments", a.ListPayments)
	mux.HandleFunc("GET /api/v1/admin/payments/{id}", a.GetPaymentDetail)
	mux.HandleFunc("GET /api/v1/admin/payments/{id}/attempts", a.ListPaymentAttempts)
	mux.HandleFunc("GET /api/v1/admin/payments/{id}/events", a.ListPaymentEvents)
	mux.HandleFunc("GET /api/v1/admin/payments/{id}/refunds", a.ListRefunds)
	mux.HandleFunc("POST /api/v1/admin/payments/{id}/refunds", rf.AdminCreateRefund)
	mux.HandleFunc("POST /api/v1/admin/payments/{id}/expire", a.ForceExpire)
	mux.HandleFunc("GET /api/v1/admin/outbox/stats", a.OutboxStats)
	return mux
}
