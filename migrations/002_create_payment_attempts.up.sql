CREATE TABLE payment_attempts (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_id             UUID         NOT NULL,
    gateway_transaction_id VARCHAR(255),
    attempt_number         INT          NOT NULL,
    status                 VARCHAR(50)  NOT NULL,
    error_code             VARCHAR(100),
    error_message          TEXT,
    gateway_request        JSONB,
    gateway_response       JSONB,
    http_status_code       INT,
    duration_ms            BIGINT,
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payment_attempts_payment_id ON payment_attempts (payment_id);
