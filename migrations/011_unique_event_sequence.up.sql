-- Sequence numbers used to restart at 1 on every save. Renumber existing events per
-- payment in the order they were written, then forbid duplicates.
UPDATE payment_events e
SET sequence_number = n.seq
FROM (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY aggregate_id ORDER BY created_at, sequence_number, id) AS seq
    FROM payment_events
) n
WHERE e.id = n.id;

DROP INDEX IF EXISTS idx_payment_events_aggregate;
CREATE UNIQUE INDEX uq_payment_events_sequence ON payment_events (aggregate_id, sequence_number);
