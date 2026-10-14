DROP INDEX IF EXISTS idx_refunds_pending;
DROP INDEX IF EXISTS uq_payment_attempts_number;

ALTER TABLE payment_attempts
    DROP COLUMN refund_id,
    DROP COLUMN operation;
