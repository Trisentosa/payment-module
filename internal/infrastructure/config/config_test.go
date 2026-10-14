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
