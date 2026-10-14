-- name: InsertOutboxEvent :exec
INSERT INTO outbox_events (aggregate_id, event_type, payload)
VALUES ($1, $2, $3);

-- name: GetPendingOutboxEvents :many
-- Run inside a transaction: the row locks are what keep other workers off these rows
-- until this worker has published them and committed.
SELECT id, aggregate_id, event_type, payload, retry_count, created_at
FROM outbox_events
WHERE status = 'PENDING'
  AND scheduled_at <= NOW()
ORDER BY created_at
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxEventPublished :exec
UPDATE outbox_events
SET status       = 'PUBLISHED',
    published_at = NOW()
WHERE id = $1;

-- name: MarkOutboxEventFailed :exec
UPDATE outbox_events
SET status       = CASE WHEN retry_count >= 5 THEN 'FAILED' ELSE 'PENDING' END,
    retry_count  = retry_count + 1,
    last_error   = $2,
    -- Back off 30s, 60s, 90s, ... (retry_count here is the value before this failure).
    scheduled_at = NOW() + ((retry_count + 1) * INTERVAL '30 seconds')
WHERE id = $1;
