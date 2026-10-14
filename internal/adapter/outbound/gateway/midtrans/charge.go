package midtrans

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

// capabilities follows Midtrans's refund rules: bank-transfer VAs can't be refunded through
// the API, and ShopeePay only supports full refunds.
var capabilities = map[payment.Method]ports.MethodCapability{
	payment.MethodVirtualAccount: {Charge: true},
	payment.MethodQRIS:           {Charge: true, Refund: true, PartialRefund: true},
	payment.MethodGoPay:          {Charge: true, Refund: true, PartialRefund: true},
	payment.MethodShopeePay:      {Charge: true, Refund: true},
	payment.MethodCard:           {Charge: true, Refund: true, PartialRefund: true},
}

func (a *Adapter) Capability(method payment.Method) ports.MethodCapability {
	return capabilities[method]
}

// vaBanks are the banks Midtrans issues VAs for through payment_type bank_transfer.
// Mandiri uses a different flow (echannel bill payment) and isn't supported yet.
var vaBanks = map[string]bool{"bca": true, "bni": true, "bri": true, "permata": true, "cimb": true}

// wib is Midtrans's timezone: response timestamps carry no offset and are in Jakarta time.
var wib = time.FixedZone("WIB", 7*60*60)

func (a *Adapter) CreateTransaction(ctx context.Context, req ports.CreateTransactionRequest) (*ports.GatewayResponse, error) {
	log := logger.FromContext(ctx)

	body, err := buildChargeRequest(req, time.Now())
	if err != nil {
		return nil, err
	}

	res, err := a.do(ctx, http.MethodPost, "/v2/charge", body)
	if err != nil {
		return nil, apperror.GatewayError("midtrans charge failed", err)
	}

	switch {
	case res.code == "400":
		// Midtrans validated the request and refused it; nothing was created.
		return nil, apperror.InvalidInput(fmt.Sprintf("midtrans rejected the charge: %s %v", res.message(), res.body["validation_messages"]))
	case res.failed():
		return nil, apperror.GatewayError(fmt.Sprintf("midtrans charge failed: %s %s", res.code, res.message()), unknownIfServerError(res))
	}

	txID, _ := res.body["transaction_id"].(string)
	if txID == "" {
		return nil, apperror.GatewayError("missing transaction_id in midtrans response", nil)
	}

	txStatus, _ := res.body["transaction_status"].(string)
	fraudStatus, _ := res.body["fraud_status"].(string)
	status := mapTransactionStatus(txStatus, fraudStatus)

	log.Info("midtrans transaction created",
		"gateway_tx_id", txID, "payment_id", req.PaymentID, "method", req.PaymentMethod, "gateway_status", txStatus)

	return &ports.GatewayResponse{
		GatewayTransactionID: txID,
		Status:               status,
		NextAction:           nextAction(req.PaymentMethod, status, res.body),
		RawResponse:          res.body,
	}, nil
}

// buildChargeRequest maps a gateway-neutral request onto Midtrans's /v2/charge body.
// Problems the caller can fix come back as INVALID_INPUT, before anything reaches Midtrans.
func buildChargeRequest(req ports.CreateTransactionRequest, now time.Time) (map[string]any, error) {
	body := map[string]any{
		"transaction_details": map[string]any{
			// order_id is PayGate's payment ID, so it is globally unique and webhooks map
			// straight back to a payment. Callers' references can collide across services.
			"order_id":     req.PaymentID,
			"gross_amount": req.Amount,
		},
		// Keeps the caller's reference searchable in the Midtrans dashboard.
		"custom_field1": req.ReferenceID,
		"customer_details": map[string]any{
			"first_name": req.CustomerName,
			"email":      req.CustomerEmail,
			"phone":      req.CustomerPhone,
		},
	}

	switch req.PaymentMethod {
	case payment.MethodVirtualAccount:
		bank := strings.ToLower(req.BankCode)
		if !vaBanks[bank] {
			return nil, apperror.InvalidInput("bank_code must be one of BCA, BNI, BRI, PERMATA, CIMB for VIRTUAL_ACCOUNT")
		}
		body["payment_type"] = "bank_transfer"
		body["bank_transfer"] = map[string]any{"bank": bank}
	case payment.MethodQRIS:
		body["payment_type"] = "qris"
		body["qris"] = map[string]any{"acquirer": "gopay"}
	case payment.MethodGoPay:
		body["payment_type"] = "gopay"
		if req.ReturnURL != "" {
			body["gopay"] = map[string]any{"enable_callback": true, "callback_url": req.ReturnURL}
		}
	case payment.MethodShopeePay:
		if req.ReturnURL == "" {
			return nil, apperror.InvalidInput("return_url is required for SHOPEEPAY")
		}
		body["payment_type"] = "shopeepay"
		body["shopeepay"] = map[string]any{"callback_url": req.ReturnURL}
	case payment.MethodCard:
		if req.CardToken == "" {
			return nil, apperror.InvalidInput("card_token is required for CARD; tokenize the card with Midtrans.js first")
		}
		body["payment_type"] = "credit_card"
		body["credit_card"] = map[string]any{"token_id": req.CardToken, "authentication": true}
	default:
		return nil, apperror.InvalidInput("MIDTRANS does not support payment_method_type " + string(req.PaymentMethod))
	}

	// Cards finish (or fail) at 3DS time; every other method waits for the customer until it expires.
	if req.ExpiredAt != nil && req.PaymentMethod != payment.MethodCard {
		minutes := int(math.Ceil(req.ExpiredAt.Sub(now).Minutes()))
		if minutes < 1 {
			return nil, apperror.InvalidInput("expired_at must be at least one minute in the future")
		}
		body["custom_expiry"] = map[string]any{
			"order_time":      now.In(wib).Format("2006-01-02 15:04:05 -0700"),
			"expiry_duration": minutes,
			"unit":            "minute",
		}
	}

	return body, nil
}

// nextAction turns Midtrans's method-specific response into what the customer does next.
func nextAction(method payment.Method, status ports.TransactionStatus, body map[string]any) ports.NextAction {
	if status != ports.TransactionPending {
		return ports.NextAction{Type: ports.NextActionNone}
	}

	na := ports.NextAction{ExpiresAt: parseWIB(body["expiry_time"])}
	actions := actionURLs(body)

	switch method {
	case payment.MethodVirtualAccount:
		na.Type = ports.NextActionDisplayVA
		if permata, ok := body["permata_va_number"].(string); ok && permata != "" {
			na.VANumber, na.Bank = permata, "permata"
		} else if vas, ok := body["va_numbers"].([]any); ok && len(vas) > 0 {
			if va, ok := vas[0].(map[string]any); ok {
				na.VANumber, _ = va["va_number"].(string)
				na.Bank, _ = va["bank"].(string)
			}
		}
	case payment.MethodQRIS:
		na.Type = ports.NextActionDisplayQR
		na.QRString, _ = body["qr_string"].(string)
		na.QRImageURL = actions["generate-qr-code"]
	case payment.MethodGoPay:
		// Deeplink into the Gojek app on mobile; the QR image serves desktop customers.
		na.Type = ports.NextActionRedirect
		na.RedirectURL = actions["deeplink-redirect"]
		na.QRImageURL = actions["generate-qr-code"]
	case payment.MethodShopeePay:
		na.Type = ports.NextActionRedirect
		na.RedirectURL = actions["deeplink-redirect"]
	case payment.MethodCard:
		// Pending card charges need 3DS authentication at redirect_url.
		na.Type = ports.NextActionRedirect
		na.RedirectURL, _ = body["redirect_url"].(string)
	}
	return na
}

func actionURLs(body map[string]any) map[string]string {
	out := map[string]string{}
	actions, _ := body["actions"].([]any)
	for _, raw := range actions {
		a, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := a["name"].(string)
		url, _ := a["url"].(string)
		if name != "" && url != "" {
			out[name] = url
		}
	}
	return out
}

func parseWIB(v any) *time.Time {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, wib)
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}
