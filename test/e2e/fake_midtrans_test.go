//go:build e2e

package e2e_test

import (
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMidtrans stands in for the Midtrans Core API. It answers charge, status, cancel and
// refund calls with the shapes from Midtrans's docs, and sends signed HTTP notifications to
// PayGate the way Midtrans does.
//
// Behavior is chosen by the payment's reference_id, which PayGate forwards as custom_field1:
//   - containing "LOST": the charge is created but the reply comes after PayGate's timeout.
//   - containing "DENY": the charge is declined (status_code 202, transaction_status deny).
type fakeMidtrans struct {
	srv        *httptest.Server
	serverKey  string
	webhookURL string
	replyDelay time.Duration // for LOST charges; longer than PayGate's MIDTRANS_TIMEOUT_MS

	mu      sync.Mutex
	byOrder map[string]*fakeTx
	byTxID  map[string]*fakeTx
	seq     int
}

type fakeTx struct {
	OrderID       string
	TransactionID string
	PaymentType   string
	GrossAmount   int64
	Status        string // pending, settlement, capture, cancel, deny, ...
	Cancelled     bool
	Refunds       []fakeRefund
}

type fakeRefund struct {
	Key          string
	ChargebackID int
	Amount       int64
}

// newFakeMidtrans starts the fake. Close it with Close.
func newFakeMidtrans(serverKey string, replyDelay time.Duration) *fakeMidtrans {
	f := &fakeMidtrans{
		serverKey:  serverKey,
		replyDelay: replyDelay,
		byOrder:    map[string]*fakeTx{},
		byTxID:     map[string]*fakeTx{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v2/charge", f.charge)
	mux.HandleFunc("GET /v2/{id}/status", f.status)
	mux.HandleFunc("POST /v2/{id}/cancel", f.cancel)
	mux.HandleFunc("POST /v2/{id}/refund", f.refund)
	f.srv = httptest.NewServer(f.requireServerKey(mux))
	return f
}

func (f *fakeMidtrans) URL() string { return f.srv.URL }

func (f *fakeMidtrans) Close() { f.srv.Close() }

func (f *fakeMidtrans) requireServerKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, _, ok := r.BasicAuth(); !ok || user != f.serverKey {
			reply(w, http.StatusUnauthorized, map[string]any{"status_code": "401", "status_message": "Access denied"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func reply(w http.ResponseWriter, httpStatus int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeMidtrans) charge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaymentType        string `json:"payment_type"`
		CustomField1       string `json:"custom_field1"`
		TransactionDetails struct {
			OrderID     string `json:"order_id"`
			GrossAmount int64  `json:"gross_amount"`
		} `json:"transaction_details"`
		BankTransfer struct {
			Bank string `json:"bank"`
		} `json:"bank_transfer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		reply(w, http.StatusOK, map[string]any{"status_code": "400", "status_message": "invalid JSON"})
		return
	}

	f.mu.Lock()
	if _, dup := f.byOrder[req.TransactionDetails.OrderID]; dup {
		f.mu.Unlock()
		reply(w, http.StatusOK, map[string]any{"status_code": "406", "status_message": "The request could not be completed due to a conflict with the current state of the target resource, please try again"})
		return
	}
	f.seq++
	tx := &fakeTx{
		OrderID:       req.TransactionDetails.OrderID,
		TransactionID: fmt.Sprintf("fake-tx-%04d", f.seq),
		PaymentType:   req.PaymentType,
		GrossAmount:   req.TransactionDetails.GrossAmount,
		Status:        "pending",
	}
	if strings.Contains(req.CustomField1, "DENY") {
		tx.Status = "deny"
	}
	f.byOrder[tx.OrderID], f.byTxID[tx.TransactionID] = tx, tx
	created := *tx // the reply describes the charge as created, even if it changes meanwhile
	f.mu.Unlock()
	tx = &created

	if strings.Contains(req.CustomField1, "LOST") {
		time.Sleep(f.replyDelay) // PayGate gives up first; the transaction exists anyway
	}

	body := map[string]any{
		"status_code":        "201",
		"status_message":     "Success, transaction is created",
		"transaction_id":     tx.TransactionID,
		"order_id":           tx.OrderID,
		"gross_amount":       fmt.Sprintf("%d.00", tx.GrossAmount),
		"payment_type":       tx.PaymentType,
		"transaction_status": tx.Status,
		"fraud_status":       "accept",
		"expiry_time":        time.Now().Add(24 * time.Hour).In(time.FixedZone("WIB", 7*3600)).Format("2006-01-02 15:04:05"),
	}
	if tx.Status == "deny" {
		body["status_code"], body["status_message"] = "202", "Deny by Bank [MANDIRI] with code [05] and message [Do not honour]"
	}
	switch req.PaymentType {
	case "bank_transfer":
		body["va_numbers"] = []map[string]any{{"bank": req.BankTransfer.Bank, "va_number": "8123" + tx.TransactionID[len(tx.TransactionID)-4:]}}
	case "qris":
		body["qr_string"] = "00020101021226620014COM.GO-JEK.WWW" + tx.TransactionID
		body["actions"] = []map[string]any{{"name": "generate-qr-code", "method": "GET", "url": f.srv.URL + "/qr/" + tx.TransactionID}}
	case "gopay", "shopeepay":
		body["actions"] = []map[string]any{
			{"name": "generate-qr-code", "method": "GET", "url": f.srv.URL + "/qr/" + tx.TransactionID},
			{"name": "deeplink-redirect", "method": "GET", "url": "gojek://gopay/merchanttransfer?tref=" + tx.TransactionID},
		}
	case "credit_card":
		body["redirect_url"] = f.srv.URL + "/3ds/" + tx.TransactionID
	}
	reply(w, http.StatusOK, body)
}

func (f *fakeMidtrans) find(id string) *fakeTx {
	if tx, ok := f.byTxID[id]; ok {
		return tx
	}
	return f.byOrder[id]
}

func (f *fakeMidtrans) status(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tx := f.find(r.PathValue("id"))
	if tx == nil {
		reply(w, http.StatusNotFound, map[string]any{"status_code": "404", "status_message": "Transaction doesn't exist."})
		return
	}
	reply(w, http.StatusOK, f.notificationBody(tx))
}

func (f *fakeMidtrans) cancel(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	tx := f.find(r.PathValue("id"))
	if tx == nil {
		f.mu.Unlock()
		reply(w, http.StatusOK, map[string]any{"status_code": "404", "status_message": "Transaction doesn't exist."})
		return
	}
	if tx.Status != "pending" {
		f.mu.Unlock()
		reply(w, http.StatusOK, map[string]any{"status_code": "412", "status_message": "Merchant cannot modify the status of the transaction"})
		return
	}
	tx.Status, tx.Cancelled = "cancel", true
	body := f.notificationBody(tx)
	f.mu.Unlock()
	body["status_message"] = "Success, transaction is canceled"
	reply(w, http.StatusOK, body)
}

func (f *fakeMidtrans) refund(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefundKey string `json:"refund_key"`
		Amount    int64  `json:"amount"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	f.mu.Lock()
	defer f.mu.Unlock()
	tx := f.find(r.PathValue("id"))
	switch {
	case tx == nil:
		reply(w, http.StatusOK, map[string]any{"status_code": "404", "status_message": "Transaction doesn't exist."})
		return
	case tx.Status != "settlement" && tx.Status != "capture" && tx.Status != "partial_refund":
		reply(w, http.StatusOK, map[string]any{"status_code": "412", "status_message": "Merchant cannot modify the status of the transaction"})
		return
	}
	for _, rf := range tx.Refunds {
		if rf.Key == req.RefundKey {
			reply(w, http.StatusOK, map[string]any{"status_code": "406", "status_message": "Duplicate refund ID"})
			return
		}
	}
	var refunded int64
	for _, rf := range tx.Refunds {
		refunded += rf.Amount
	}
	if req.Amount <= 0 || refunded+req.Amount > tx.GrossAmount {
		reply(w, http.StatusOK, map[string]any{"status_code": "414", "status_message": "Refund amount is invalid"})
		return
	}
	f.seq++
	tx.Refunds = append(tx.Refunds, fakeRefund{Key: req.RefundKey, ChargebackID: f.seq, Amount: req.Amount})
	if refunded+req.Amount == tx.GrossAmount {
		tx.Status = "refund"
	} else {
		tx.Status = "partial_refund"
	}
	reply(w, http.StatusOK, map[string]any{
		"status_code":          "200",
		"status_message":       "Success, refund request is approved",
		"transaction_id":       tx.TransactionID,
		"order_id":             tx.OrderID,
		"refund_chargeback_id": f.seq,
		"refund_amount":        fmt.Sprintf("%d.00", req.Amount),
		"refund_key":           req.RefundKey,
		"transaction_status":   tx.Status,
	})
}

// notificationBody is the transaction as Midtrans reports it in status replies and HTTP
// notifications, signed with the server key.
func (f *fakeMidtrans) notificationBody(tx *fakeTx) map[string]any {
	statusCode := "201"
	switch tx.Status {
	case "settlement", "capture", "refund", "partial_refund", "cancel":
		statusCode = "200"
	case "deny":
		statusCode = "202"
	case "expire":
		statusCode = "407"
	}
	gross := fmt.Sprintf("%d.00", tx.GrossAmount)
	sum := sha512.Sum512([]byte(tx.OrderID + statusCode + gross + f.serverKey))
	body := map[string]any{
		"status_code":        statusCode,
		"transaction_id":     tx.TransactionID,
		"order_id":           tx.OrderID,
		"gross_amount":       gross,
		"payment_type":       tx.PaymentType,
		"transaction_status": tx.Status,
		"fraud_status":       "accept",
		"signature_key":      hex.EncodeToString(sum[:]),
	}
	if len(tx.Refunds) > 0 {
		var refunds []map[string]any
		for _, rf := range tx.Refunds {
			refunds = append(refunds, map[string]any{
				"refund_chargeback_id": rf.ChargebackID, // a number, as Midtrans really sends it
				"refund_amount":        fmt.Sprintf("%d.00", rf.Amount),
				"refund_key":           rf.Key,
			})
		}
		body["refunds"] = refunds
	}
	return body
}

// Pay marks the order paid (settlement, or capture for cards) and notifies PayGate.
func (f *fakeMidtrans) Pay(t *testing.T, orderID string) {
	t.Helper()
	f.mu.Lock()
	tx := f.byOrder[orderID]
	if tx == nil {
		f.mu.Unlock()
		t.Fatalf("fake midtrans: no transaction for order %s", orderID)
	}
	tx.Status = "settlement"
	if tx.PaymentType == "credit_card" {
		tx.Status = "capture"
	}
	f.mu.Unlock()
	f.Notify(t, orderID, http.StatusOK)
}

// Notify sends the order's current state to PayGate's webhook and checks the reply status.
func (f *fakeMidtrans) Notify(t *testing.T, orderID string, wantStatus int) {
	t.Helper()
	f.mu.Lock()
	tx := f.byOrder[orderID]
	if tx == nil {
		f.mu.Unlock()
		t.Fatalf("fake midtrans: no transaction for order %s", orderID)
	}
	body := f.notificationBody(tx)
	f.mu.Unlock()
	f.post(t, body, wantStatus)
}

func (f *fakeMidtrans) post(t *testing.T, body map[string]any, wantStatus int) {
	t.Helper()
	b, _ := json.Marshal(body)
	res, err := http.Post(f.webhookURL, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("send notification: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != wantStatus {
		t.Fatalf("notification for %v: PayGate answered %d, want %d", body["order_id"], res.StatusCode, wantStatus)
	}
}

// Tx returns a copy of the order's transaction, or nil.
func (f *fakeMidtrans) Tx(orderID string) *fakeTx {
	f.mu.Lock()
	defer f.mu.Unlock()
	tx := f.byOrder[orderID]
	if tx == nil {
		return nil
	}
	c := *tx
	c.Refunds = append([]fakeRefund(nil), tx.Refunds...)
	return &c
}
