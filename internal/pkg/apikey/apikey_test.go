package apikey_test

import (
	"strings"
	"testing"

	"github.com/Trisentosa/payment-module/internal/pkg/apikey"
)

func TestGenerate_IsPrefixedAndUnique(t *testing.T) {
	a, err := apikey.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, _ := apikey.Generate()
	if !strings.HasPrefix(a, apikey.Prefix) || len(a) < 40 || a == b {
		t.Errorf("keys: %q %q", a, b)
	}
}

func TestParseHashes(t *testing.T) {
	h1, h2, h3 := apikey.Hash("k1"), apikey.Hash("k2"), apikey.Hash("k3")
	got, err := apikey.ParseHashes(" order-service:" + h1 + ", billing-service:" + strings.ToUpper(h2) + ",order-service:" + h3 + ",")
	if err != nil {
		t.Fatalf("ParseHashes: %v", err)
	}
	want := map[string]string{h1: "order-service", h2: "billing-service", h3: "order-service"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for h, svc := range want {
		if got[h] != svc {
			t.Errorf("%s: got %q, want %q", h, got[h], svc)
		}
	}
}

func TestParseHashes_Rejects(t *testing.T) {
	h := apikey.Hash("k")
	for name, in := range map[string]string{
		"no separator":        "order-service" + h,
		"no service":          ":" + h,
		"plaintext key":       "order-service:pgk_abc",
		"short hash":          "order-service:abcd",
		"hash on two service": "a:" + h + ",b:" + h,
	} {
		if _, err := apikey.ParseHashes(in); err == nil {
			t.Errorf("%s: expected an error for %q", name, in)
		}
	}
}
