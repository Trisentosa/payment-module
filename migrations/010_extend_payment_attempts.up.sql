-- payment_attempts now records every outbound gateway call for a payment: charges,
-- cancels, refunds and status checks. `status` holds the outcome
-- (SUCCEEDED / FAILED / UNKNOWN).
ALTER TABLE payment_attempts
    ADD COLUMN operation VARCHAR(20) NOT NULL DEFAULT 'CHARGE',
    ADD COLUMN refund_id UUID;

CREATE UNIQUE INDEX uq_payment_attempts_number ON payment_attempts (payment_id, attempt_number);

-- Lets the reconciler find refunds still waiting for the gateway's answer.
CREATE INDEX idx_refunds_pending ON refunds (created_at)
    WHERE status = 'PENDING' AND deleted_at IS NULL;
