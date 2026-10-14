package payment

import (
	"strings"

	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

// Method is how the customer pays, independent of which gateway carries the payment.
// Whether a given gateway can charge or refund a method is the gateway adapter's call.
type Method string

const (
	MethodVirtualAccount Method = "VIRTUAL_ACCOUNT"
	MethodQRIS           Method = "QRIS"
	MethodGoPay          Method = "GOPAY"
	MethodShopeePay      Method = "SHOPEEPAY"
	MethodCard           Method = "CARD"
)

var knownMethods = map[Method]bool{
	MethodVirtualAccount: true,
	MethodQRIS:           true,
	MethodGoPay:          true,
	MethodShopeePay:      true,
	MethodCard:           true,
}

// ParseMethod validates a caller-supplied method name.
func ParseMethod(s string) (Method, error) {
	m := Method(strings.ToUpper(strings.TrimSpace(s)))
	if !knownMethods[m] {
		return "", apperror.InvalidInput("unsupported payment_method_type: " + s)
	}
	return m, nil
}
