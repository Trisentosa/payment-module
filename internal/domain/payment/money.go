package payment

import "fmt"

type Money struct {
	Amount   int64
	Currency string
}

func NewMoney(amount int64, currency string) (Money, error) {
	if amount <= 0 {
		return Money{}, fmt.Errorf("amount must be positive, got %d", amount)
	}
	if currency == "" {
		return Money{}, fmt.Errorf("currency is required")
	}
	return Money{Amount: amount, Currency: currency}, nil
}
