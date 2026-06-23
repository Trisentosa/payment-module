CREATE TABLE refunds (
    id                       UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_id               UUID         NOT NULL,
    reference_id             VARCHAR(255) NOT NULL,
    gateway_refund_id        VARCHAR(255),
    status                   VARCHAR(50)  NOT NULL DEFAULT 'REQUESTED',
    amount                   BIGINT       NOT NULL CHECK (amount > 0),
    reason                   TEXT,
    gateway_response_payload JSONB,
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at               TIMESTAMPTZ,

    CONSTRAINT uq_refund_reference UNIQUE (reference_id, payment_id)
);

CREATE INDEX idx_refunds_payment_id ON refunds (payment_id);
