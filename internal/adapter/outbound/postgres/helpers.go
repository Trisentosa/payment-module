package postgres

import (
	"encoding/json"

	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

func eventPayload(evt payment.DomainEvent) []byte {
	b, _ := json.Marshal(evt)
	return b
}

func marshalJSONFields(p *payment.Payment) (customerInfoJSON, metadataJSON []byte, err error) {
	customerInfoJSON, err = json.Marshal(p.CustomerInfo)
	if err != nil {
		return nil, nil, apperror.Internal("marshal customer_info", err)
	}
	if p.Metadata != nil {
		metadataJSON, err = json.Marshal(p.Metadata)
		if err != nil {
			return nil, nil, apperror.Internal("marshal metadata", err)
		}
	}
	return customerInfoJSON, metadataJSON, nil
}

func toNullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
