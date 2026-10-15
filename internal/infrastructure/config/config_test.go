package config_test

import (
	"strings"
	"testing"

	"github.com/Trisentosa/payment-module/internal/infrastructure/config"
	"github.com/Trisentosa/payment-module/internal/pkg/apikey"
)

const adminKey = "test-admin-key-with-at-least-32-chars"

func TestLoad_ParsesCallerKeys(t *testing.T) {
	t.Setenv("ADMIN_API_KEY", adminKey)
	t.Setenv("CALLER_API_KEYS", "order-service:"+apikey.Hash("k1")+",billing-service:"+apikey.Hash("k2"))

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Callers.KeyHashes[apikey.Hash("k1")] != "order-service" || cfg.Callers.KeyHashes[apikey.Hash("k2")] != "billing-service" {
		t.Errorf("key hashes: %v", cfg.Callers.KeyHashes)
	}
}

func TestLoad_RefusesToStartWithoutSecrets(t *testing.T) {
	validCallers := "order-service:" + apikey.Hash("k1")
	cases := []struct {
		name, admin, callers, wantInErr string
	}{
		{"no admin key", "", validCallers, "ADMIN_API_KEY"},
		{"short admin key", "too-short", validCallers, "ADMIN_API_KEY"},
		{"no caller keys", adminKey, "", "CALLER_API_KEYS"},
		{"plaintext caller key", adminKey, "order-service:pgk_secret", "CALLER_API_KEYS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ADMIN_API_KEY", tc.admin)
			t.Setenv("CALLER_API_KEYS", tc.callers)
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), tc.wantInErr) {
				t.Fatalf("expected an error about %s, got %v", tc.wantInErr, err)
			}
		})
	}
}

func TestLoad_ReconcileTiming(t *testing.T) {
	t.Setenv("ADMIN_API_KEY", adminKey)
	t.Setenv("CALLER_API_KEYS", "order-service:"+apikey.Hash("k1"))

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if cfg.Reconcile.Interval.String() != "1m0s" || cfg.Reconcile.ChargeGrace.String() != "2m0s" {
		t.Errorf("defaults: %+v", cfg.Reconcile)
	}

	cases := []struct{ name, timeoutMs, grace, wantInErr string }{
		{"grace not longer than the gateway timeout", "30000", "30s", "RECONCILE_CHARGE_GRACE"},
		{"unparseable grace", "30000", "soon", "RECONCILE_CHARGE_GRACE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MIDTRANS_TIMEOUT_MS", tc.timeoutMs)
			t.Setenv("RECONCILE_CHARGE_GRACE", tc.grace)
			if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("got %v, want an error naming %s", err, tc.wantInErr)
			}
		})
	}
}

func TestLoad_MidtransBaseURLOverride(t *testing.T) {
	t.Setenv("ADMIN_API_KEY", adminKey)
	t.Setenv("CALLER_API_KEYS", "order-service:"+apikey.Hash("k1"))
	t.Setenv("MIDTRANS_ENV", "production")
	t.Setenv("MIDTRANS_BASE_URL", "http://127.0.0.1:9999")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Midtrans.BaseURL != "http://127.0.0.1:9999" {
		t.Errorf("base URL: %s", cfg.Midtrans.BaseURL)
	}
}

func TestLoad_QRISAcquirer(t *testing.T) {
	t.Setenv("ADMIN_API_KEY", adminKey)
	t.Setenv("CALLER_API_KEYS", "order-service:"+apikey.Hash("k1"))

	for value, wantErr := range map[string]bool{"": false, "gopay": false, "airpay shopee": false, "shopee": true, "dana": true} {
		t.Setenv("MIDTRANS_QRIS_ACQUIRER", value)
		cfg, err := config.Load()
		if (err != nil) != wantErr {
			t.Errorf("%q: err=%v, want error %v", value, err, wantErr)
		}
		if value == "" && err == nil && cfg.Midtrans.QRISAcquirer != "gopay" {
			t.Errorf("default acquirer: %q", cfg.Midtrans.QRISAcquirer)
		}
	}
}
