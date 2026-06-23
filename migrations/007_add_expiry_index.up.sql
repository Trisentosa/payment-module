CREATE INDEX idx_payments_expiry
    ON payments (expired_at, status)
    WHERE status IN ('PENDING', 'INITIATED')
      AND deleted_at IS NULL
      AND expired_at IS NOT NULL;
