-- name: InsertOutboxEvent :exec
INSERT INTO outbox_events (aggregate_id, event_type, payload)
VALUES ($1, $2, $3);

-- name: GetPendingOutboxEvents :many
SELECT id, aggregate_id, event_type, payload, retry_count
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
    scheduled_at = NOW() + (retry_count * INTERVAL '30 seconds')
WHERE id = $1;
