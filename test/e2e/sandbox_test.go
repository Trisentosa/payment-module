//go:build sandbox

package e2e_test

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The sandbox drill: real transactions against the Midtrans sandbox. Virtual accounts and
// ShopeePay are paid through Midtrans's simulators automatically (simulator_test.go); the
// rest, and everything with SANDBOX_MANUAL=1, waits for a person to pay when asked. Each scenario checks
// PayGate's API, its events, and Midtrans's own status API for the same transaction.
//
// Run all of it with `make sandbox`, or one scenario with
//   make sandbox SANDBOX_RUN=TestSandbox_QRIS

func humanWait() time.Duration {
	d, err := time.ParseDuration(envOr("SANDBOX_WAIT", "10m"))
	if err != nil {
		return 10 * time.Minute
	}
	return d
}

// action prints what the person running the drill must do now.
func action(t *testing.T, lines ...string) {
	t.Helper()
	fmt.Printf("\n==> ACTION NEEDED (%s)\n", t.Name())
	for _, l := range lines {
		fmt.Println("    " + l)
	}
	fmt.Printf("    Waiting up to %s for Midtrans's notification...\n\n", humanWait())
}

// payOrAsk pays through the simulator when pay is given and SANDBOX_MANUAL isn't set;
// otherwise it asks the person, with lines saying how.
func payOrAsk(t *testing.T, pay func() error, lines ...string) {
	t.Helper()
	if pay != nil && os.Getenv("SANDBOX_MANUAL") == "" {
		if err := pay(); err != nil {
			t.Fatalf("paying through the simulator: %v (SANDBOX_MANUAL=1 pays by hand)", err)
		}
		t.Log("paid through the Midtrans simulator")
		return
	}
	action(t, lines...)
}

func needWebhooks(t *testing.T) {
	t.Helper()
	if !webhooks {
		t.Skip("needs Midtrans's notifications to reach PayGate (no tunnel)")
	}
}

// waitPaid waits until Midtrans's notification has moved the payment to want.
func waitPaid(t *testing.T, id, want string) {
	t.Helper()
	waitForStatus(t, orderKey, id, want, humanWait())
}

// gatewayStatus reads Midtrans's own transaction_status for the payment.
func gatewayStatus(t *testing.T, id string) map[string]any {
	t.Helper()
	m, err := midtransStatus(id)
	if err != nil {
		t.Fatalf("midtrans status: %v", err)
	}
	return m
}

// expectGatewayStatus waits until Midtrans reports one of the wanted transaction statuses.
func expectGatewayStatus(t *testing.T, id string, within time.Duration, want ...string) map[string]any {
	t.Helper()
	var last map[string]any
	eventually(t, within, "Midtrans status "+strings.Join(want, "/"), func() (bool, string) {
		last = gatewayStatus(t, id)
		got := str(last, "transaction_status")
		for _, w := range want {
			if got == w {
				return true, ""
			}
		}
		return false, fmt.Sprintf("transaction_status %q (status_code %s)", got, str(last, "status_code"))
	})
	return last
}

// refund asks PayGate for a refund and checks that its answer agrees with Midtrans.
// It returns the refund status PayGate settled on.
func refund(t *testing.T, id, reference string, amount int64) (string, map[string]any) {
	t.Helper()
	body := map[string]any{"reference_id": reference, "reason": "sandbox drill"}
	if amount > 0 {
		body["amount"] = amount
	}
	r := call(t, http.MethodPost, "/payments/"+id+"/refunds", orderKey, body)
	if r.Status != http.StatusCreated && r.Status != http.StatusAccepted {
		t.Fatalf("refund %s: status %d %s", reference, r.Status, r.Body)
	}
	res := r.JSON(t)
	status := str(res, "refund.status")
	t.Logf("refund %s: %s %s", reference, status, str(res, "refund.failure_reason"))

	// Midtrans refunds from the merchant's payable balance. A sandbox account with none
	// declines every refund (414); PayGate must still record that cleanly.
	if status == "FAILED" && strings.Contains(str(res, "refund.failure_reason"), "insufficient funds") {
		if str(res, "payment.status") == "REFUNDED" || num(res, "payment.refundable_amount") == 0 {
			t.Errorf("a declined refund must leave the amount refundable: %v", res)
		}
		t.Skipf("Midtrans declined the refund for lack of merchant balance (%s); PayGate recorded it correctly. "+
			"Refunds can be tested once the sandbox account has a payable balance.", str(res, "refund.failure_reason"))
	}

	if status == "SUCCEEDED" {
		gw := gatewayStatus(t, id)
		listed := false
		for _, raw := range asSlice(gw["refunds"]) {
			if m, ok := raw.(map[string]any); ok && m["refund_key"] == str(res, "refund.refund_id") {
				listed = true
			}
		}
		if !listed {
			t.Errorf("PayGate says SUCCEEDED but Midtrans doesn't list refund_key %s: %v", str(res, "refund.refund_id"), gw["refunds"])
		}
	}
	return status, res
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// ---- scenarios that need no person ----

func TestSandbox_CardTokenAndCharge(t *testing.T) {
	tok, err := cardToken("4811 1111 1111 1114")
	if err != nil {
		t.Fatalf("card token: %v", err)
	}
	p := create(t, paymentRequest(ref("SBX-CARD-PROBE"), "CARD", 10000, map[string]any{"card_token": tok}))
	id := str(p, "payment_id")
	if str(p, "status") != "PENDING" || str(p, "next_action.type") != "REDIRECT" || str(p, "next_action.redirect_url") == "" {
		t.Fatalf("card charge: %v", p)
	}
	gw := gatewayStatus(t, id)
	t.Logf("Midtrans sees %s/%s, 3DS at %s", str(gw, "transaction_status"), str(gw, "fraud_status"), str(p, "next_action.redirect_url"))
	expect(t, call(t, http.MethodDelete, "/payments/"+id, orderKey, nil), http.StatusOK, "cancel the unpaid card charge")
	expectGatewayStatus(t, id, 30*time.Second, "cancel", "deny", "expire")
}

func TestSandbox_VirtualAccountPermata_Cancel(t *testing.T) {
	p := create(t, paymentRequest(ref("SBX-PERMATA"), "VIRTUAL_ACCOUNT", 25000, map[string]any{"bank_code": "PERMATA"}))
	id := str(p, "payment_id")
	if str(p, "next_action.type") != "DISPLAY_VA" || str(p, "next_action.bank") != "permata" || str(p, "next_action.va_number") == "" {
		t.Fatalf("Permata VA instructions (Permata returns its number in a different field): %v", p)
	}
	expectGatewayStatus(t, id, 30*time.Second, "pending")

	expect(t, call(t, http.MethodDelete, "/payments/"+id, orderKey, nil), http.StatusOK, "cancel")
	expectGatewayStatus(t, id, 30*time.Second, "cancel")
	events.WaitFor(t, id, "payment.cancelled")
	if webhooks {
		time.Sleep(10 * time.Second) // Midtrans's own cancel notification follows
		if got := paymentStatus(t, orderKey, id); got != "CANCELLED" {
			t.Errorf("after Midtrans's cancel notification: %s, want CANCELLED", got)
		}
	}
}

func TestSandbox_CardDeclined(t *testing.T) {
	for name, card := range map[string]string{"by bank": "4911 1111 1111 1113", "by fraud check": "4611 1111 1111 1116"} {
		t.Run(name, func(t *testing.T) {
			tok, err := cardToken(card)
			if err != nil {
				t.Fatalf("card token: %v", err)
			}
			p := create(t, paymentRequest(ref("SBX-DENY-"+strings.ReplaceAll(name, " ", "-")), "CARD", 10000, map[string]any{"card_token": tok}))
			id := str(p, "payment_id")
			if str(p, "status") == "FAILED" {
				events.WaitFor(t, id, "payment.failed")
				return
			}
			needWebhooks(t)
			action(t, "Open the 3-D Secure page and enter OTP 112233; the card will be declined:",
				str(p, "next_action.redirect_url"))
			waitPaid(t, id, "FAILED")
			expectGatewayStatus(t, id, 30*time.Second, "deny", "cancel", "failure")
		})
	}
}

// ---- scenarios where a person pays in the simulator ----

func TestSandbox_VirtualAccount_PaidNotRefundable(t *testing.T) {
	needWebhooks(t)
	simulators := map[string]string{
		"bca":     "https://simulator.sandbox.midtrans.com/bca/va/index",
		"bni":     "https://simulator.sandbox.midtrans.com/bni/va/index",
		"permata": "https://simulator.sandbox.midtrans.com/openapi/va/index?bank=permata",
		"bri":     "https://simulator.sandbox.midtrans.com/openapi/va/index?bank=bri",
	}
	for _, bank := range []string{"bca", "bni", "permata", "bri"} {
		t.Run(bank, func(t *testing.T) {
			p := create(t, paymentRequest(ref("SBX-VA-"+bank), "VIRTUAL_ACCOUNT", 50000, map[string]any{"bank_code": strings.ToUpper(bank)}))
			id, va := str(p, "payment_id"), str(p, "next_action.va_number")
			if str(p, "next_action.bank") != bank || va == "" {
				t.Fatalf("VA instructions: %v", p)
			}
			payOrAsk(t, func() error { return newSimulator().payVA(bank, va) },
				"Pay this "+strings.ToUpper(bank)+" virtual account in the simulator:",
				"  "+simulators[bank],
				"  VA number: "+va+"   amount: Rp50.000")
			waitPaid(t, id, "COMPLETED")
			expectGatewayStatus(t, id, 30*time.Second, "settlement")
			completed := events.WaitFor(t, id, "payment.completed")
			if completed.Data["paid_at"] == nil {
				t.Errorf("payment.completed without paid_at: %v", completed.Data)
			}

			expect(t, call(t, http.MethodPost, "/payments/"+id+"/refunds", orderKey, map[string]any{"reference_id": ref("SBX-VA-RF-" + bank)}),
				http.StatusUnprocessableEntity, "VA refund")
		})
	}
}

func TestSandbox_QRIS_PartialRefunds(t *testing.T) {
	needWebhooks(t)
	p := create(t, paymentRequest(ref("SBX-QRIS"), "QRIS", 100000, nil))
	id := str(p, "payment_id")
	payOrAsk(t, qrisPayer(str(p, "next_action.qr_image_url"), "airpay shopee"),
		"Pay this QRIS code in the simulator (paste the image URL):",
		"  https://simulator.sandbox.midtrans.com/qris/index",
		"  QR image URL: "+str(p, "next_action.qr_image_url"))
	waitPaid(t, id, "COMPLETED")
	expectGatewayStatus(t, id, 30*time.Second, "settlement")

	if status, _ := refund(t, id, ref("SBX-QRIS-RF1"), 40000); status != "SUCCEEDED" {
		t.Fatalf("partial QRIS refund: %s", status)
	}
	expectGatewayStatus(t, id, 60*time.Second, "partial_refund")
	if got := paymentStatus(t, orderKey, id); got != "PARTIALLY_REFUNDED" {
		t.Errorf("after partial refund: %s", got)
	}
	if status, _ := refund(t, id, ref("SBX-QRIS-RF2"), 0); status != "SUCCEEDED" {
		t.Fatalf("refund of the rest: %s", status)
	}
	expectGatewayStatus(t, id, 60*time.Second, "refund")
	if got := paymentStatus(t, orderKey, id); got != "REFUNDED" {
		t.Errorf("after full refund: %s", got)
	}
	eventually(t, 30*time.Second, "two refund.completed events", func() (bool, string) {
		n := events.count(id, "refund.completed")
		return n == 2, fmt.Sprint(n)
	})
}

// qrisPayer pays the QR automatically when the ShopeePay acquirer issued it; GoPay-acquired
// QRs are left to the person, since they fail in the simulator on many sandbox accounts.
func qrisPayer(qrImageURL, issuer string) func() error {
	if qrisAcquirer != "airpay shopee" {
		return nil
	}
	return func() error { return newSimulator().payQRIS(qrImageURL, issuer) }
}

// QRIS is interoperable: any QRIS app can pay the code, and Midtrans reports which one did.
func TestSandbox_QRIS_AnyAppPays(t *testing.T) {
	needWebhooks(t)
	for _, app := range []struct{ issuer, reported string }{
		{"gopay", "gopay"}, {"dana", "dana"}, {"ovo", "ovo"}, {"tcash", "linkaja"},
	} {
		t.Run(app.issuer, func(t *testing.T) {
			p := create(t, paymentRequest(ref("SBX-QRIS-"+app.issuer), "QRIS", 12000, nil))
			id := str(p, "payment_id")
			payOrAsk(t, qrisPayer(str(p, "next_action.qr_image_url"), app.issuer),
				"Pay this QRIS code with "+app.issuer+" in the simulator (paste the image URL):",
				"  https://simulator.sandbox.midtrans.com/qris/index",
				"  QR image URL: "+str(p, "next_action.qr_image_url"))
			waitPaid(t, id, "COMPLETED")
			gw := expectGatewayStatus(t, id, 30*time.Second, "settlement")
			if !strings.EqualFold(str(gw, "issuer"), app.reported) || str(gw, "acquirer") != qrisAcquirer {
				t.Errorf("Midtrans reports issuer %q acquirer %q, want %q and %q", str(gw, "issuer"), str(gw, "acquirer"), app.reported, qrisAcquirer)
			}
			events.WaitFor(t, id, "payment.completed")
		})
	}
}

func TestSandbox_GoPay_PartialRefund(t *testing.T) {
	needWebhooks(t)
	p := create(t, paymentRequest(ref("SBX-GOPAY"), "GOPAY", 60000, map[string]any{"return_url": "https://example.com/return"}))
	id := str(p, "payment_id")
	action(t, "Pay with GoPay in the deeplink simulator:",
		"  https://simulator.sandbox.midtrans.com/v2/deeplink/index",
		"  deeplink: "+str(p, "next_action.redirect_url"),
		"  (or paste this QR image URL into https://simulator.sandbox.midtrans.com/v2/qris/index:",
		"   "+str(p, "next_action.qr_image_url")+")")
	waitPaid(t, id, "COMPLETED")
	expectGatewayStatus(t, id, 30*time.Second, "settlement")

	if status, _ := refund(t, id, ref("SBX-GOPAY-RF1"), 20000); status != "SUCCEEDED" {
		t.Fatalf("partial GoPay refund: %s", status)
	}
	expectGatewayStatus(t, id, 60*time.Second, "partial_refund")
}

func TestSandbox_ShopeePay_FullRefundOnly(t *testing.T) {
	needWebhooks(t)
	p := create(t, paymentRequest(ref("SBX-SPAY"), "SHOPEEPAY", 35000, map[string]any{"return_url": "https://example.com/return"}))
	id := str(p, "payment_id")
	payOrAsk(t, func() error { return newSimulator().payShopeePay(str(p, "next_action.redirect_url")) },
		"Pay with ShopeePay: open this link and confirm with PIN 123456:",
		"  "+str(p, "next_action.redirect_url"))
	waitPaid(t, id, "COMPLETED")
	expectGatewayStatus(t, id, 30*time.Second, "settlement")

	expect(t, call(t, http.MethodPost, "/payments/"+id+"/refunds", orderKey, map[string]any{"reference_id": ref("SBX-SPAY-RF1"), "amount": 5000}),
		http.StatusBadRequest, "partial ShopeePay refund")
	if status, _ := refund(t, id, ref("SBX-SPAY-RF2"), 0); status != "SUCCEEDED" {
		t.Fatalf("full ShopeePay refund: %s", status)
	}
	expectGatewayStatus(t, id, 60*time.Second, "refund")
}

func TestSandbox_Card_3DSThenRefund(t *testing.T) {
	needWebhooks(t)
	tok, err := cardToken("4811 1111 1111 1114")
	if err != nil {
		t.Fatalf("card token: %v", err)
	}
	p := create(t, paymentRequest(ref("SBX-CARD"), "CARD", 80000, map[string]any{"card_token": tok}))
	id := str(p, "payment_id")
	action(t, "Open the 3-D Secure page and enter OTP 112233:", str(p, "next_action.redirect_url"))
	waitPaid(t, id, "COMPLETED")
	gw := expectGatewayStatus(t, id, 30*time.Second, "capture", "settlement")
	events.WaitFor(t, id, "payment.completed")

	// Midtrans refunds only settled card payments, and captures settle later (next day in
	// production). Whatever Midtrans answers, PayGate must record it consistently.
	status, res := refund(t, id, ref("SBX-CARD-RF1"), 30000)
	switch status {
	case "SUCCEEDED":
		expectGatewayStatus(t, id, 60*time.Second, "partial_refund")
	case "FAILED":
		if num(res, "payment.refundable_amount") != 80000 || str(res, "payment.status") != "COMPLETED" {
			t.Errorf("a declined refund must release its amount: %v", res)
		}
		t.Logf("Midtrans declined the card refund while the capture is %q: %s. Expected until it settles; retry tomorrow.",
			str(gw, "transaction_status"), str(res, "refund.failure_reason"))
	default:
		t.Errorf("card refund left %s", status)
	}
}

// Midtrans expires the VA at expired_at: checks the Jakarta-time custom_expiry PayGate sends.
func TestSandbox_VirtualAccount_ExpiresOnTime(t *testing.T) {
	expiresAt := time.Now().Add(2 * time.Minute).Truncate(time.Second)
	p := create(t, paymentRequest(ref("SBX-EXPIRE"), "VIRTUAL_ACCOUNT", 15000,
		map[string]any{"bank_code": "BNI", "expired_at": expiresAt.UTC().Format(time.RFC3339)}))
	id := str(p, "payment_id")

	got, err := time.Parse(time.RFC3339, str(p, "next_action.expires_at"))
	if err != nil {
		t.Fatalf("next_action.expires_at: %v (%v)", err, p)
	}
	// Midtrans counts whole minutes from the order time, so allow a minute either way.
	if diff := got.Sub(expiresAt); math.Abs(diff.Seconds()) > 60 {
		t.Errorf("Midtrans will expire it at %s, asked for %s (off by %s: a time zone bug?)", got, expiresAt, diff)
	}
	t.Logf("waiting for the VA to expire at %s", got.Local().Format("15:04:05"))
	expectGatewayStatus(t, id, time.Until(got)+2*time.Minute, "expire")
	waitForStatus(t, orderKey, id, "EXPIRED", 2*time.Minute)
	events.WaitFor(t, id, "payment.expired")
}
