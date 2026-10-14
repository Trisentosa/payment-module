ALTER TABLE refunds
    ALTER COLUMN status SET DEFAULT 'REQUESTED',
    DROP COLUMN completed_at,
    DROP COLUMN requested_by,
    DROP COLUMN failure_reason;

ALTER TABLE payments DROP COLUMN version;
