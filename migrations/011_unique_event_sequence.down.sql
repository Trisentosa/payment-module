DROP INDEX IF EXISTS uq_payment_events_sequence;
CREATE INDEX idx_payment_events_aggregate ON payment_events (aggregate_id, sequence_number);
