-- name: GetPaymentAttemptsByPayment :many
SELECT id, payment_id, gateway_transaction_id, attempt_number, status, error_code, error_message,
       gateway_request, gateway_response, http_status_code, duration_ms, created_at
FROM payment_attempts
WHERE payment_id = $1
ORDER BY attempt_number ASC;

-- name: GetPaymentEventsByPayment :many
SELECT id, aggregate_id, aggregate_type, event_type, sequence_number, payload, created_by, created_at
FROM payment_events
WHERE aggregate_id = $1
ORDER BY sequence_number ASC;

-- name: GetRefundsByPayment :many
SELECT id, payment_id, reference_id, gateway_refund_id, status, amount, reason,
       gateway_response_payload, created_at, updated_at, deleted_at
FROM refunds
WHERE payment_id = $1 AND deleted_at IS NULL;

-- name: GetOutboxStats :many
SELECT
    status,
    COUNT(*)::int AS count,
    COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(created_at)))::int, 0) AS oldest_age_seconds
FROM outbox_events
WHERE created_at > NOW() - INTERVAL '1 hour'
GROUP BY status;
