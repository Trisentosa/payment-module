package redis

import (
	"time"

	"github.com/Trisentosa/payment-module/internal/domain/payment"
)

var statusTTL = map[payment.Status]time.Duration{
	payment.StatusInitiated:         30 * time.Second,
	payment.StatusPending:           60 * time.Second,
	payment.StatusProcessing:        30 * time.Second,
	payment.StatusCompleted:         time.Hour,
	payment.StatusFailed:            time.Hour,
	payment.StatusCancelled:         time.Hour,
	payment.StatusExpired:           time.Hour,
	payment.StatusPartiallyRefunded: time.Hour,
	payment.StatusRefunded:          time.Hour,
}

func TTLForStatus(s payment.Status) time.Duration {
	if ttl, ok := statusTTL[s]; ok {
		return ttl
	}
	return 60 * time.Second
}
