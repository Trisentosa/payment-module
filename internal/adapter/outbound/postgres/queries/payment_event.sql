-- name: InsertPaymentEvent :exec
INSERT INTO payment_events (aggregate_id, aggregate_type, event_type, sequence_number, payload)
VALUES ($1, 'PAYMENT', $2, $3, $4);

-- name: GetLastEventSequence :one
SELECT COALESCE(MAX(sequence_number), 0)::int AS last_sequence
FROM payment_events
WHERE aggregate_id = $1;
