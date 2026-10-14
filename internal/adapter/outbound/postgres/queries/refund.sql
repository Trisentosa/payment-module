-- name: ListRefundsByPayment :many
SELECT id, payment_id, reference_id, gateway_refund_id, status, amount, reason,
       failure_reason, requested_by, created_at, updated_at, completed_at
FROM refunds
WHERE payment_id = $1 AND deleted_at IS NULL
ORDER BY created_at, id;

-- name: UpsertRefund :exec
INSERT INTO refunds (
    id, payment_id, reference_id, gateway_refund_id, status, amount, reason,
    failure_reason, requested_by, created_at, updated_at, completed_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
ON CONFLICT (id) DO UPDATE
SET gateway_refund_id = EXCLUDED.gateway_refund_id,
    status            = EXCLUDED.status,
    failure_reason    = EXCLUDED.failure_reason,
    updated_at        = EXCLUDED.updated_at,
    completed_at      = EXCLUDED.completed_at;
