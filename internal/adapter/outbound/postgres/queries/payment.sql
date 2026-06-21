-- name: GetPaymentByID :one
SELECT id, reference_id, caller_service, gateway_type, gateway_transaction_id,
       status, amount, currency, payment_method_type,
       customer_info, metadata, gateway_request_payload, gateway_response_payload,
       description, expired_at, paid_at, created_at, updated_at, deleted_at
FROM payments
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetPaymentByReference :one
SELECT id, reference_id, caller_service, gateway_type, gateway_transaction_id,
       status, amount, currency, payment_method_type,
       customer_info, metadata, gateway_request_payload, gateway_response_payload,
       description, expired_at, paid_at, created_at, updated_at, deleted_at
FROM payments
WHERE reference_id = $1 AND caller_service = $2 AND deleted_at IS NULL;

-- name: GetPaymentByGatewayTransactionID :one
SELECT id, reference_id, caller_service, gateway_type, gateway_transaction_id,
       status, amount, currency, payment_method_type,
       customer_info, metadata, gateway_request_payload, gateway_response_payload,
       description, expired_at, paid_at, created_at, updated_at, deleted_at
FROM payments
WHERE gateway_transaction_id = $1 AND deleted_at IS NULL;

-- name: GetExpiredPayments :many
SELECT id, reference_id, caller_service, gateway_type, gateway_transaction_id,
       status, amount, currency, payment_method_type,
       customer_info, metadata, gateway_request_payload, gateway_response_payload,
       description, expired_at, paid_at, created_at, updated_at, deleted_at
FROM payments
WHERE status IN ('PENDING', 'INITIATED')
  AND expired_at < NOW()
  AND deleted_at IS NULL
LIMIT $1;

-- name: InsertPayment :exec
INSERT INTO payments (
    id, reference_id, caller_service, gateway_type, gateway_transaction_id,
    status, amount, currency, payment_method_type,
    customer_info, metadata, gateway_request_payload, gateway_response_payload,
    description, expired_at, paid_at, created_at, updated_at
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18
) ON CONFLICT (reference_id, caller_service) DO NOTHING;

-- name: UpdatePaymentStatus :exec
UPDATE payments SET status = $1, updated_at = NOW() WHERE id = $2;
