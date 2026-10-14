-- Optimistic locking: every payment save checks and bumps version, so two concurrent
-- writers (e.g. two refund requests, or a webhook and a cancel) can't overwrite each other.
ALTER TABLE payments ADD COLUMN version INT NOT NULL DEFAULT 1;

ALTER TABLE refunds
    ADD COLUMN failure_reason TEXT,
    ADD COLUMN requested_by   VARCHAR(100) NOT NULL DEFAULT '',
    ADD COLUMN completed_at   TIMESTAMPTZ,
    ALTER COLUMN status SET DEFAULT 'PENDING';
