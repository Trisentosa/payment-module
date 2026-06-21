package payment_test

import (
	"testing"

	"github.com/Trisentosa/payment-module/internal/domain/payment"
)

func TestNewMoney(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		currency string
		wantErr  bool
	}{
		{"valid IDR", 10000, "IDR", false},
		{"zero amount", 0, "IDR", true},
		{"negative amount", -1, "IDR", true},
		{"empty currency", 5000, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := payment.NewMoney(tt.amount, tt.currency)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Amount != tt.amount {
				t.Errorf("amount: got %d, want %d", m.Amount, tt.amount)
			}
			if m.Currency != tt.currency {
				t.Errorf("currency: got %s, want %s", m.Currency, tt.currency)
			}
		})
	}
}
