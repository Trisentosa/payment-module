CREATE TABLE idempotency_keys (
    key           VARCHAR(64)  PRIMARY KEY,
    payment_id    UUID         NOT NULL,
    http_status   INT          NOT NULL,
    response_body JSONB        NOT NULL,
    expires_at    TIMESTAMPTZ  NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_idempotency_expires ON idempotency_keys (expires_at);
