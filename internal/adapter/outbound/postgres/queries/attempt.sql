-- name: InsertPaymentAttempt :exec
-- attempt_number counts this payment's gateway calls from 1. Two concurrent inserts can
-- pick the same number; the unique index rejects one and the caller retries.
INSERT INTO payment_attempts (
    payment_id, refund_id, operation, attempt_number, status,
    gateway_transaction_id, error_code, error_message, gateway_response, duration_ms
)
SELECT sqlc.arg(payment_id)::uuid, sqlc.narg(refund_id)::uuid, sqlc.arg(operation)::varchar,
       COALESCE(MAX(attempt_number), 0) + 1, sqlc.arg(status)::varchar,
       sqlc.narg(gateway_transaction_id)::varchar, sqlc.narg(error_code)::varchar,
       sqlc.narg(error_message)::text, sqlc.narg(gateway_response)::jsonb, sqlc.arg(duration_ms)::bigint
FROM payment_attempts
WHERE payment_id = sqlc.arg(payment_id)::uuid;
