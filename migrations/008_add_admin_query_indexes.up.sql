-- Supports status + date range filter (most common ops query)
CREATE INDEX idx_payments_admin_filter
    ON payments (status, created_at DESC)
    WHERE deleted_at IS NULL;

-- Supports text search on reference_id (ops frequently look up by caller's ref)
CREATE INDEX idx_payments_reference_id_text
    ON payments (reference_id text_pattern_ops)
    WHERE deleted_at IS NULL;
