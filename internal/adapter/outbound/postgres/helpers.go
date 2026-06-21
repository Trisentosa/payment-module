package postgres

import (
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

const selectPaymentCols = `
	id, reference_id, caller_service, gateway_type, gateway_transaction_id,
	status, amount, currency, payment_method_type,
	customer_info, metadata, gateway_request_payload, gateway_response_payload,
	description, expired_at, paid_at, created_at, updated_at, deleted_at`

func scanPayment(row pgx.Row) (*payment.Payment, error) {
	var (
		p               payment.Payment
		gatewayTxID     *string
		amount          int64
		currency        string
		customerInfoRaw []byte
		metadataRaw     []byte
		gwReqRaw        []byte
		gwRespRaw       []byte
	)

	err := row.Scan(
		&p.ID, &p.ReferenceID, &p.CallerService, &p.GatewayType, &gatewayTxID,
		&p.Status, &amount, &currency, &p.PaymentMethodType,
		&customerInfoRaw, &metadataRaw, &gwReqRaw, &gwRespRaw,
		&p.Description, &p.ExpiredAt, &p.PaidAt, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt,
	)
	if err != nil {
		return nil, err
	}

	if gatewayTxID != nil {
		p.GatewayTransactionID = *gatewayTxID
	}
	p.Amount = payment.Money{Amount: amount, Currency: currency}

	if err := json.Unmarshal(customerInfoRaw, &p.CustomerInfo); err != nil {
		return nil, err
	}
	if metadataRaw != nil {
		if err := json.Unmarshal(metadataRaw, &p.Metadata); err != nil {
			return nil, err
		}
	}
	if gwReqRaw != nil {
		if err := json.Unmarshal(gwReqRaw, &p.GatewayRequestPayload); err != nil {
			return nil, err
		}
	}
	if gwRespRaw != nil {
		if err := json.Unmarshal(gwRespRaw, &p.GatewayResponsePayload); err != nil {
			return nil, err
		}
	}

	return &p, nil
}

func scanPayments(rows pgx.Rows) ([]*payment.Payment, error) {
	var results []*payment.Payment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, p)
	}
	return results, rows.Err()
}

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
