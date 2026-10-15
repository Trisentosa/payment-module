//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// runID keeps reference_ids unique if the suite runs more than once against one database.
var runID = time.Now().Format("150405")

func ref(name string) string { return name + "-" + runID }

func paymentRequest(reference, method string, amount int64, extra map[string]any) map[string]any {
	req := map[string]any{
		"reference_id":        reference,
		"amount":              amount,
		"currency":            "IDR",
		"gateway_type":        "MIDTRANS",
		"payment_method_type": method,
		"customer_name":       "Siti",
		"customer_email":      "siti@example.com",
	}
	for k, v := range extra {
		req[k] = v
	}
	return req
}

// create makes a payment as order-service and returns the decoded 201 response.
func create(t *testing.T, req map[string]any) map[string]any {
	t.Helper()
	return expect(t, call(t, http.MethodPost, "/payments", orderKey, req), http.StatusCreated, "create payment").JSON(t)
}

func TestCallerAuthentication(t *testing.T) {
	p := create(t, paymentRequest(ref("AUTH"), "QRIS", 50000, nil))
	id := str(p, "payment_id")

	expect(t, call(t, http.MethodGet, "/payments/"+id, "", nil), http.StatusUnauthorized, "no key")
	expect(t, call(t, http.MethodGet, "/payments/"+id, "pgk_not-a-real-key", nil), http.StatusUnauthorized, "unknown key")
	expect(t, call(t, http.MethodGet, "/payments/"+id, orderKey, nil, "X-Service-Name", "billing-service"),
		http.StatusForbidden, "header naming another service")
	expect(t, call(t, http.MethodGet, "/payments/"+id, billKey, nil), http.StatusNotFound, "another service's payment")
	expect(t, call(t, http.MethodDelete, "/payments/"+id, billKey, nil), http.StatusNotFound, "cancel another service's payment")
	expect(t, admin(t, http.MethodGet, "/api/v1/admin/payments/"+id), http.StatusOK, "admin sees every payment")
	expect(t, call(t, http.MethodGet, "/api/v1/admin/payments/"+id, "", nil, "X-Admin-Key", "wrong"),
		http.StatusUnauthorized, "wrong admin key")
}

// A QRIS payment from creation through payment and two partial refunds, checked at the
// API, on RabbitMQ, at the gateway and in the database.
func TestQRIS_PaidThenRefundedInTwoParts(t *testing.T) {
	reference := ref("QRIS")
	req := paymentRequest(reference, "QRIS", 150000, nil)

	first := call(t, http.MethodPost, "/payments", orderKey, req)
	p := expect(t, first, http.StatusCreated, "create").JSON(t)
	id := str(p, "payment_id")
	if str(p, "status") != "PENDING" || str(p, "next_action.type") != "DISPLAY_QR" || str(p, "next_action.qr_string") == "" {
		t.Fatalf("create response: %s", first.Body)
	}

	// A retry of the same request replays the stored response without charging again.
	again := expect(t, call(t, http.MethodPost, "/payments", orderKey, req), http.StatusCreated, "retry")
	if again.Header.Get("X-Idempotent-Replay") != "true" || string(again.Body) != string(first.Body) {
		t.Errorf("retry was not a replay: header=%q body=%s", again.Header.Get("X-Idempotent-Replay"), again.Body)
	}
	byRef := expect(t, call(t, http.MethodGet, "/payments/ref/"+reference, orderKey, nil), http.StatusOK, "get by reference").JSON(t)
	if str(byRef, "id") != id {
		t.Errorf("by reference: got %s, want %s", str(byRef, "id"), id)
	}

	midtrans.Pay(t, id)
	if got := paymentStatus(t, orderKey, id); got != "COMPLETED" {
		t.Fatalf("after settlement: %s", got)
	}
	// Midtrans repeats notifications; a duplicate must change nothing.
	midtrans.Notify(t, id, http.StatusOK)

	refund := func(refRef string, amount int64) (int, map[string]any) {
		r := call(t, http.MethodPost, "/payments/"+id+"/refunds", orderKey,
			map[string]any{"reference_id": refRef, "amount": amount, "reason": "item out of stock"})
		return r.Status, r.JSON(t)
	}
	status, r1 := refund(ref("RF1"), 40000)
	if status != http.StatusCreated || str(r1, "refund.status") != "SUCCEEDED" || str(r1, "payment.status") != "PARTIALLY_REFUNDED" ||
		num(r1, "payment.refundable_amount") != 110000 {
		t.Fatalf("first refund: %d %v", status, r1)
	}
	if status, again := refund(ref("RF1"), 40000); status != http.StatusOK || str(again, "refund.refund_id") != str(r1, "refund.refund_id") {
		t.Errorf("repeated refund reference: %d %v, want 200 with the same refund", status, again)
	}
	status, r2 := refund(ref("RF2"), 0) // 0 = the rest
	if status != http.StatusCreated || num(r2, "refund.amount") != 110000 || str(r2, "payment.status") != "REFUNDED" {
		t.Fatalf("second refund: %d %v", status, r2)
	}
	if status, _ := refund(ref("RF3"), 1); status != http.StatusUnprocessableEntity {
		t.Errorf("refund of a fully refunded payment: %d, want 422", status)
	}

	// Midtrans's refund notification lists both refunds; PayGate already has them.
	midtrans.Notify(t, id, http.StatusOK)

	tx := midtrans.Tx(id)
	if len(tx.Refunds) != 2 || tx.Refunds[0].Key != str(r1, "refund.refund_id") || tx.Refunds[1].Amount != 110000 {
		t.Errorf("gateway refunds: %+v (refund_key must be the refund ID)", tx.Refunds)
	}

	list := expect(t, call(t, http.MethodGet, "/payments/"+id+"/refunds", orderKey, nil), http.StatusOK, "list refunds").JSON(t)
	if refunds, _ := list["data"].([]any); len(refunds) != 2 {
		t.Errorf("refund list: %v", list)
	}

	for _, rk := range []string{"payment.initiated", "payment.pending", "payment.completed", "refund.requested", "refund.completed"} {
		events.WaitFor(t, id, rk)
	}
	completed := events.WaitFor(t, id, "payment.completed")
	if completed.EventID == "" || completed.MessageID != completed.EventID || completed.Source != "paygate-service" ||
		completed.Data["payment_id"] != id || completed.Data["paid_at"] == nil {
		t.Errorf("payment.completed envelope: %+v", completed)
	}
	eventually(t, 20*time.Second, "both refund.completed events", func() (bool, string) {
		n := events.count(id, "refund.completed")
		return n == 2, fmt.Sprintf("%d received", n)
	})
	for _, e := range events.For(id) {
		if e.RoutingKey == "refund.completed" && e.Data["refund_id"] == str(r2, "refund.refund_id") {
			if e.Data["payment_status"] != "REFUNDED" || e.Data["refunded_amount"] != float64(150000) {
				t.Errorf("last refund.completed data: %v", e.Data)
			}
		}
	}
	if n := events.count(id, "payment.completed"); n != 1 {
		t.Errorf("payment.completed published %d times; the duplicate notification must not raise another", n)
	}

	// The event history is numbered 1..n with no gaps, in one sequence across saves.
	var count, maxSeq int
	if err := db.QueryRow(context.Background(),
		`SELECT count(*), coalesce(max(sequence_number), 0) FROM payment_events WHERE aggregate_id = $1`, id).Scan(&count, &maxSeq); err != nil {
		t.Fatal(err)
	}
	if count != 7 || maxSeq != 7 {
		t.Errorf("payment_events: %d rows, max sequence %d; want 7 and 7", count, maxSeq)
	}

	detail := expect(t, admin(t, http.MethodGet, "/api/v1/admin/payments/"+id), http.StatusOK, "admin detail").JSON(t)
	ops := map[string]int{}
	for _, a := range detail["attempts"].([]any) {
		ops[str(a.(map[string]any), "operation")]++
	}
	if ops["CHARGE"] != 1 || ops["REFUND"] != 2 || str(detail, "payment.status") != "REFUNDED" {
		t.Errorf("admin detail: attempts by operation %v, payment status %s", ops, str(detail, "payment.status"))
	}
}

func TestVirtualAccount_PaidButNotRefundable(t *testing.T) {
	expect(t, call(t, http.MethodPost, "/payments", orderKey,
		paymentRequest(ref("VA-NOBANK"), "VIRTUAL_ACCOUNT", 200000, map[string]any{"bank_code": "XYZ"})),
		http.StatusBadRequest, "unsupported bank")

	p := create(t, paymentRequest(ref("VA"), "VIRTUAL_ACCOUNT", 200000, map[string]any{"bank_code": "BCA"}))
	id := str(p, "payment_id")
	if str(p, "next_action.type") != "DISPLAY_VA" || str(p, "next_action.bank") != "bca" || str(p, "next_action.va_number") == "" {
		t.Fatalf("VA instructions: %v", p)
	}
	if str(p, "payment_instructions.va_number") != str(p, "next_action.va_number") {
		t.Errorf("legacy payment_instructions out of step with next_action: %v", p)
	}

	midtrans.Pay(t, id)
	r := call(t, http.MethodPost, "/payments/"+id+"/refunds", orderKey, map[string]any{"reference_id": ref("VA-RF")})
	expect(t, r, http.StatusUnprocessableEntity, "VA refund")
	if len(midtrans.Tx(id).Refunds) != 0 {
		t.Error("a rejected VA refund reached the gateway")
	}
}

func TestCard_3DSRedirectThenCapture(t *testing.T) {
	expect(t, call(t, http.MethodPost, "/payments", orderKey, paymentRequest(ref("CARD-NOTOKEN"), "CARD", 99000, nil)),
		http.StatusBadRequest, "card without token")

	p := create(t, paymentRequest(ref("CARD"), "CARD", 99000, map[string]any{"card_token": "481111-1114-fake-token"}))
	id := str(p, "payment_id")
	if str(p, "next_action.type") != "REDIRECT" || !strings.Contains(str(p, "next_action.redirect_url"), "/3ds/") {
		t.Fatalf("3DS redirect: %v", p)
	}
	midtrans.Pay(t, id) // capture with fraud_status accept
	if got := paymentStatus(t, orderKey, id); got != "COMPLETED" {
		t.Errorf("after capture: %s", got)
	}
}

func TestShopeePay_ReturnURLAndFullRefundOnly(t *testing.T) {
	expect(t, call(t, http.MethodPost, "/payments", orderKey, paymentRequest(ref("SPAY-NOURL"), "SHOPEEPAY", 75000, nil)),
		http.StatusBadRequest, "ShopeePay without return_url")

	p := create(t, paymentRequest(ref("SPAY"), "SHOPEEPAY", 75000, map[string]any{"return_url": "https://shop.example/return"}))
	id := str(p, "payment_id")
	if str(p, "next_action.type") != "REDIRECT" || str(p, "next_action.redirect_url") == "" {
		t.Fatalf("ShopeePay redirect: %v", p)
	}
	midtrans.Pay(t, id)
	expect(t, call(t, http.MethodPost, "/payments/"+id+"/refunds", orderKey, map[string]any{"reference_id": ref("SPAY-RF1"), "amount": 1000}),
		http.StatusBadRequest, "partial ShopeePay refund")
	full := expect(t, call(t, http.MethodPost, "/payments/"+id+"/refunds", orderKey, map[string]any{"reference_id": ref("SPAY-RF2")}),
		http.StatusCreated, "full ShopeePay refund").JSON(t)
	if str(full, "payment.status") != "REFUNDED" {
		t.Errorf("after full refund: %v", full)
	}
}

func TestDeclinedCharge(t *testing.T) {
	r := call(t, http.MethodPost, "/payments", orderKey,
		paymentRequest(ref("CARD-DENY"), "CARD", 99000, map[string]any{"card_token": "tok"}))
	p := expect(t, r, http.StatusCreated, "declined card").JSON(t)
	if str(p, "status") != "FAILED" || str(p, "next_action.type") != "NONE" {
		t.Errorf("declined charge: %s", r.Body)
	}
	events.WaitFor(t, str(p, "payment_id"), "payment.failed")
}

// Regression: Midtrans's cancel notification used to turn a CANCELLED payment into FAILED.
func TestGoPay_CancelThenGatewayCancelNotification(t *testing.T) {
	p := create(t, paymentRequest(ref("GOPAY"), "GOPAY", 30000, map[string]any{"return_url": "https://shop.example/return"}))
	id := str(p, "payment_id")
	if str(p, "next_action.type") != "REDIRECT" || !strings.HasPrefix(str(p, "next_action.redirect_url"), "gojek://") {
		t.Fatalf("GoPay deeplink: %v", p)
	}

	c := expect(t, call(t, http.MethodDelete, "/payments/"+id, orderKey, nil), http.StatusOK, "cancel").JSON(t)
	if str(c, "status") != "CANCELLED" || !midtrans.Tx(id).Cancelled {
		t.Fatalf("cancel: %v, cancelled at gateway: %v", c, midtrans.Tx(id).Cancelled)
	}
	midtrans.Notify(t, id, http.StatusOK) // transaction_status "cancel"
	if got := paymentStatus(t, orderKey, id); got != "CANCELLED" {
		t.Errorf("after the gateway's cancel notification: %s, want CANCELLED", got)
	}
	expect(t, call(t, http.MethodDelete, "/payments/"+id, orderKey, nil), http.StatusUnprocessableEntity, "second cancel")
	events.WaitFor(t, id, "payment.cancelled")
	if n := events.count(id, "payment.failed"); n != 0 {
		t.Errorf("payment.failed published %d times for a cancelled payment", n)
	}
}

func TestWebhook_RejectsBadSignature(t *testing.T) {
	p := create(t, paymentRequest(ref("SIG"), "QRIS", 10000, nil))
	id := str(p, "payment_id")
	tx := midtrans.Tx(id)

	forged := map[string]any{
		"order_id": id, "transaction_id": tx.TransactionID, "status_code": "200", "gross_amount": "10000.00",
		"transaction_status": "settlement", "fraud_status": "accept", "signature_key": strings.Repeat("ab", 64),
	}
	midtrans.post(t, forged, http.StatusBadRequest)
	if got := paymentStatus(t, orderKey, id); got != "PENDING" {
		t.Errorf("after a forged settlement: %s, want PENDING", got)
	}
}

// Concurrent identical requests must charge once: one wins, the rest replay it or are told
// to retry.
func TestCreate_ConcurrentDuplicatesChargeOnce(t *testing.T) {
	req := paymentRequest(ref("RACE"), "QRIS", 20000, nil)
	const n = 8
	statuses := make([]int, n)
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := call(t, http.MethodPost, "/payments", orderKey, req)
			statuses[i] = r.Status
			if r.Status == http.StatusCreated {
				ids[i] = str(r.JSON(t), "payment_id")
			}
		}()
	}
	wg.Wait()

	created := map[string]bool{}
	for i, s := range statuses {
		switch s {
		case http.StatusCreated:
			created[ids[i]] = true
		case http.StatusConflict:
		default:
			t.Errorf("request %d: status %d", i, s)
		}
	}
	if len(created) != 1 {
		t.Fatalf("payments created: %v (statuses %v), want exactly one", created, statuses)
	}
	var charges int
	if err := db.QueryRow(context.Background(),
		`SELECT count(*) FROM payment_attempts a JOIN payments p ON p.id = a.payment_id
		  WHERE p.reference_id = $1 AND a.operation = 'CHARGE'`, req["reference_id"]).Scan(&charges); err != nil {
		t.Fatal(err)
	}
	if charges != 1 {
		t.Errorf("gateway charges: %d, want 1", charges)
	}
}

// The charge reply never arrives. The payment waits as INITIATED, then the reconciler asks
// Midtrans: a pending charge the customer never saw is cancelled; a captured card is kept.
func TestLostChargeReply_Reconciled(t *testing.T) {
	t.Run("pending charge is cancelled", func(t *testing.T) {
		reference := ref("LOST-QRIS")
		r := call(t, http.MethodPost, "/payments", orderKey, paymentRequest(reference, "QRIS", 45000, nil))
		expect(t, r, http.StatusBadGateway, "charge with lost reply")

		p := expect(t, call(t, http.MethodGet, "/payments/ref/"+reference, orderKey, nil), http.StatusOK, "find by reference").JSON(t)
		id := str(p, "id")
		if str(p, "status") != "INITIATED" || !strings.Contains(string(r.Body), id) {
			t.Fatalf("after lost reply: status %s, error %s (must name the payment)", str(p, "status"), r.Body)
		}
		expect(t, call(t, http.MethodPost, "/payments", orderKey, paymentRequest(reference, "QRIS", 45000, nil)),
			http.StatusConflict, "retry with the same reference")

		waitForStatus(t, orderKey, id, "FAILED", 15*time.Second)
		if !midtrans.Tx(id).Cancelled {
			t.Error("the orphaned charge was not cancelled at the gateway")
		}
		failed := events.WaitFor(t, id, "payment.failed")
		if failed.Data["error_code"] != "CHARGE_REPLY_LOST" {
			t.Errorf("payment.failed data: %v", failed.Data)
		}
	})

	t.Run("captured card is completed", func(t *testing.T) {
		reference := ref("LOST-CARD")
		expect(t, call(t, http.MethodPost, "/payments", orderKey,
			paymentRequest(reference, "CARD", 120000, map[string]any{"card_token": "tok"})),
			http.StatusBadGateway, "charge with lost reply")
		id := str(expect(t, call(t, http.MethodGet, "/payments/ref/"+reference, orderKey, nil), http.StatusOK, "find").JSON(t), "id")

		midtrans.Pay(t, id) // the customer finished 3DS; the capture notification arrives first
		if got := paymentStatus(t, orderKey, id); got != "COMPLETED" {
			t.Errorf("after capture notification: %s, want COMPLETED", got)
		}
	})

	t.Run("captured card without notification is completed by the reconciler", func(t *testing.T) {
		reference := ref("LOST-CARD2")
		expect(t, call(t, http.MethodPost, "/payments", orderKey,
			paymentRequest(reference, "CARD", 120000, map[string]any{"card_token": "tok"})),
			http.StatusBadGateway, "charge with lost reply")
		id := str(expect(t, call(t, http.MethodGet, "/payments/ref/"+reference, orderKey, nil), http.StatusOK, "find").JSON(t), "id")

		midtrans.mu.Lock()
		midtrans.byOrder[id].Status = "capture" // paid at Midtrans, notification lost too
		midtrans.mu.Unlock()

		waitForStatus(t, orderKey, id, "COMPLETED", 15*time.Second)
		detail := expect(t, admin(t, http.MethodGet, "/api/v1/admin/payments/"+id), http.StatusOK, "admin detail").JSON(t)
		var outcomes []string
		for _, a := range detail["attempts"].([]any) {
			a := a.(map[string]any)
			outcomes = append(outcomes, str(a, "operation")+":"+str(a, "status"))
		}
		if len(outcomes) < 2 || outcomes[0] != "CHARGE:UNKNOWN" || outcomes[len(outcomes)-1] != "STATUS:SUCCEEDED" {
			t.Errorf("attempts: %v, want CHARGE:UNKNOWN first and a STATUS check last", outcomes)
		}
	})
}

func TestListPayments_PagesAndScopesToCaller(t *testing.T) {
	for i := range 3 {
		expect(t, call(t, http.MethodPost, "/payments", billKey, paymentRequest(ref(fmt.Sprintf("BILL-%d", i)), "QRIS", 5000, nil)),
			http.StatusCreated, "create billing payment")
	}
	page1 := expect(t, call(t, http.MethodGet, "/payments?limit=2", billKey, nil), http.StatusOK, "page 1").JSON(t)
	cursor := str(page1, "meta.next_cursor")
	if num(page1, "meta.count") != 2 || get(page1, "meta.has_more") != true || cursor == "" {
		t.Fatalf("page 1: %v", page1)
	}
	page2 := expect(t, call(t, http.MethodGet, "/payments?limit=2&cursor="+cursor, billKey, nil), http.StatusOK, "page 2").JSON(t)
	if num(page2, "meta.count") != 1 || get(page2, "meta.has_more") != false {
		t.Errorf("page 2: %v", page2)
	}
	for _, page := range []map[string]any{page1, page2} {
		for _, p := range page["data"].([]any) {
			if str(p.(map[string]any), "caller_service") != "billing-service" {
				t.Errorf("billing-service listed another service's payment: %v", p)
			}
		}
	}
	expect(t, call(t, http.MethodGet, "/payments?cursor=nonsense", billKey, nil), http.StatusBadRequest, "bad cursor")
}

func TestAdminForceExpire(t *testing.T) {
	p := create(t, paymentRequest(ref("EXPIRE"), "QRIS", 15000, nil))
	id := str(p, "payment_id")
	expect(t, admin(t, http.MethodPost, "/api/v1/admin/payments/"+id+"/expire"), http.StatusOK, "force expire")
	if got := paymentStatus(t, orderKey, id); got != "EXPIRED" {
		t.Errorf("after force expire: %s (a stale cached view?)", got)
	}
	events.WaitFor(t, id, "payment.expired")
}

// Runs last (Go runs tests in file order): every event so far has been published.
func TestOutbox_Drained(t *testing.T) {
	eventually(t, 20*time.Second, "outbox to drain", func() (bool, string) {
		s := expect(t, admin(t, http.MethodGet, "/api/v1/admin/outbox/stats"), http.StatusOK, "outbox stats").JSON(t)
		return num(s, "pending") == 0 && num(s, "failed") == 0, fmt.Sprintf("%v", s)
	})
}
