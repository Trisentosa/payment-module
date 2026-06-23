-- name: GetIdempotencyKey :one
SELECT key, payment_id, http_status, response_body, expires_at
FROM idempotency_keys
WHERE key = $1 AND expires_at > NOW();

-- name: InsertIdempotencyKey :exec
INSERT INTO idempotency_keys (key, payment_id, http_status, response_body, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (key) DO NOTHING;
