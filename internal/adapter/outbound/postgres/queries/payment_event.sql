-- name: InsertPaymentEvent :exec
INSERT INTO payment_events (aggregate_id, aggregate_type, event_type, sequence_number, payload)
VALUES ($1, 'PAYMENT', $2, $3, $4);
