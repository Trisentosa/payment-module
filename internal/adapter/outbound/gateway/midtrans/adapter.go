package midtrans

import (
	"bytes"
	"context"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type Config struct {
	ServerKey string
	BaseURL   string
	Timeout   time.Duration
}

type Adapter struct {
	cfg    Config
	client *http.Client
}

func NewAdapter(cfg Config) *Adapter {
	return &Adapter{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

// notification holds the HTTP notification fields PayGate reads. Midtrans sends many more
// (va_numbers, payment_amounts, …), some of them arrays, so this must stay a typed struct.
type notification struct {
	TransactionID     string `json:"transaction_id"`
	OrderID           string `json:"order_id"`
	StatusCode        string `json:"status_code"`
	GrossAmount       string `json:"gross_amount"`
	SignatureKey      string `json:"signature_key"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
	// Refunds is present on refund / partial_refund notifications, one entry per refund.
	Refunds []refundEntry `json:"refunds"`
}

type refundEntry struct {
	RefundChargebackID flexString `json:"refund_chargeback_id"`
	RefundAmount       flexString `json:"refund_amount"`
	RefundKey          string     `json:"refund_key"`
}

// flexString accepts a JSON string or number. Midtrans documents refund_chargeback_id as a
// string but sends a number, and sends amounts as decimal strings ("5000.00").
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	*f = flexString(b)
	return nil
}

// amount parses a Midtrans amount such as "5000.00" into whole currency units.
func (f flexString) amount() int64 {
	v, err := strconv.ParseFloat(string(f), 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(v))
}

// ParseWebhook verifies Midtrans's body signature,
// SHA512(order_id + status_code + gross_amount + server_key), and maps the status.
func (a *Adapter) ParseWebhook(payload []byte, _ map[string][]string) (*ports.WebhookNotification, error) {
	if a.cfg.ServerKey == "" {
		// With an empty key anyone could compute a valid signature.
		return nil, apperror.InvalidInput("midtrans server key not configured; rejecting webhook")
	}

	var n notification
	if err := json.Unmarshal(payload, &n); err != nil {
		return nil, apperror.InvalidInput("invalid webhook payload")
	}

	sum := sha512.Sum512([]byte(n.OrderID + n.StatusCode + n.GrossAmount + a.cfg.ServerKey))
	expected := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(expected), []byte(n.SignatureKey)) != 1 {
		return nil, apperror.InvalidInput("webhook signature mismatch")
	}
	if n.TransactionID == "" {
		return nil, apperror.InvalidInput("webhook missing transaction_id")
	}

	refunds := make([]ports.RefundNotification, 0, len(n.Refunds))
	for _, r := range n.Refunds {
		refunds = append(refunds, ports.RefundNotification{
			RefundKey:       r.RefundKey,
			GatewayRefundID: string(r.RefundChargebackID),
			Amount:          r.RefundAmount.amount(),
		})
	}

	return &ports.WebhookNotification{
		PaymentID:            n.OrderID,
		GatewayTransactionID: n.TransactionID,
		Status:               mapTransactionStatus(n.TransactionStatus, n.FraudStatus),
		GatewayStatus:        n.TransactionStatus,
		Refunds:              refunds,
	}, nil
}

// mapTransactionStatus follows Midtrans's status guide: a card "capture" only counts as
// paid once fraud review accepts it; "challenge" stays pending until the merchant decides.
func mapTransactionStatus(status, fraudStatus string) ports.TransactionStatus {
	switch status {
	case "settlement":
		return ports.TransactionSettled
	case "capture":
		if fraudStatus == "" || fraudStatus == "accept" {
			return ports.TransactionSettled
		}
		return ports.TransactionPending
	case "pending", "authorize":
		return ports.TransactionPending
	case "deny", "cancel", "expire", "failure":
		return ports.TransactionFailed
	default:
		return ports.TransactionUnknown
	}
}

func (a *Adapter) CancelTransaction(ctx context.Context, gatewayTxID string) error {
	res, err := a.do(ctx, fmt.Sprintf("/v2/%s/cancel", gatewayTxID), nil)
	if err != nil {
		return apperror.GatewayError("midtrans cancel failed", err)
	}
	if res.failed() {
		return apperror.GatewayError(fmt.Sprintf("midtrans cancel failed: %s %s", res.code, res.message()), nil)
	}
	return nil
}

func (a *Adapter) GetTransactionStatus(ctx context.Context, gatewayTxID string) (string, error) {
	resp, err := a.get(ctx, fmt.Sprintf("/v2/%s/status", gatewayTxID))
	if err != nil {
		return "", apperror.GatewayError("midtrans status check failed", err)
	}
	status, _ := resp["transaction_status"].(string)
	return status, nil
}

// CreateRefund calls POST /v2/{transaction_id}/refund. Midtrans deduplicates on refund_key,
// so a retry after a lost reply can't refund twice.
func (a *Adapter) CreateRefund(ctx context.Context, req ports.RefundRequest) (*ports.RefundResult, error) {
	body := map[string]any{"refund_key": req.RefundKey, "amount": req.Amount}
	if req.Reason != "" {
		body["reason"] = req.Reason
	}

	res, err := a.do(ctx, fmt.Sprintf("/v2/%s/refund", req.GatewayTransactionID), body)
	if err != nil {
		return nil, apperror.GatewayError("midtrans refund: no usable reply", err)
	}

	switch {
	case res.code == "200":
		return &ports.RefundResult{Accepted: true, GatewayRefundID: fmt.Sprint(res.body["refund_chargeback_id"])}, nil
	case res.code == "406":
		// "Duplicate refund ID": an earlier attempt with this key may have gone through.
		// Leave the outcome to the refund notification instead of guessing.
		return nil, apperror.GatewayError("midtrans reports duplicate refund_key "+req.RefundKey, nil)
	case res.httpStatus >= 500 || strings.HasPrefix(res.code, "5") || strings.HasPrefix(res.code, "2"):
		return nil, apperror.GatewayError(fmt.Sprintf("midtrans refund outcome unknown: %s %s", res.code, res.message()), nil)
	default:
		// 4xx: Midtrans processed the request and refused it, e.g. 412 status can't be
		// modified, 414 invalid amount. No money moved.
		return &ports.RefundResult{Accepted: false, DeclineReason: fmt.Sprintf("midtrans %s: %s", res.code, res.message())}, nil
	}
}

// response is a decoded Midtrans reply. Midtrans often answers HTTP 200 and reports the
// outcome in the body's status_code, so callers check both.
type response struct {
	httpStatus int
	code       string // body status_code, e.g. "201" pending, "200" success, "202" denied
	body       map[string]any
}

func (r response) message() string {
	msg, _ := r.body["status_message"].(string)
	return msg
}

// failed reports a 4xx/5xx in either the HTTP status or the body's status_code.
func (r response) failed() bool {
	return r.httpStatus >= 400 || strings.HasPrefix(r.code, "4") || strings.HasPrefix(r.code, "5")
}

// do POSTs to Midtrans. An error means no usable reply arrived, so the outcome is unknown.
func (a *Adapter) do(ctx context.Context, path string, body map[string]any) (response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return response{}, err
	}
	req.SetBasicAuth(a.cfg.ServerKey, "")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	res, err := a.client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer func() { _ = res.Body.Close() }()

	out := response{httpStatus: res.StatusCode}
	if err = json.NewDecoder(res.Body).Decode(&out.body); err != nil {
		return response{}, fmt.Errorf("decode midtrans HTTP %d response: %w", res.StatusCode, err)
	}
	out.code, _ = out.body["status_code"].(string)
	if out.code == "" {
		out.code = strconv.Itoa(res.StatusCode)
	}
	return out, nil
}

func (a *Adapter) get(ctx context.Context, path string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(a.cfg.ServerKey, "")
	res, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out, nil
}
