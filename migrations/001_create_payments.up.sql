CREATE TABLE payments (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reference_id             VARCHAR(255) NOT NULL,
    caller_service           VARCHAR(100) NOT NULL,
    gateway_type             VARCHAR(50)  NOT NULL,
    gateway_transaction_id   VARCHAR(255),
    status                   VARCHAR(50)  NOT NULL DEFAULT 'INITIATED',
    amount                   BIGINT       NOT NULL CHECK (amount > 0),
    currency                 VARCHAR(10)  NOT NULL DEFAULT 'IDR',
    payment_method_type      VARCHAR(50),
    customer_info            JSONB        NOT NULL DEFAULT '{}',
    metadata                 JSONB                 DEFAULT '{}',
    gateway_request_payload  JSONB,
    gateway_response_payload JSONB,
    description              TEXT,
    expired_at               TIMESTAMPTZ,
    paid_at                  TIMESTAMPTZ,
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at               TIMESTAMPTZ,

    CONSTRAINT uq_payments_reference UNIQUE (reference_id, caller_service)
);

CREATE INDEX idx_payments_status        ON payments (status) WHERE deleted_at IS NULL;
CREATE INDEX idx_payments_gateway_tx_id ON payments (gateway_transaction_id) WHERE gateway_transaction_id IS NOT NULL;
CREATE INDEX idx_payments_created_at    ON payments (created_at);
CREATE INDEX idx_payments_caller        ON payments (caller_service, created_at);
