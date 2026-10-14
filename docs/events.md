# Events (RabbitMQ)

PayGate announces every payment and refund state change on RabbitMQ. Services that need
to react to a payment (an order service marking an order paid, say) bind a queue to the
exchange instead of polling `GET /payments/{id}`.

## Exchange and Routing Keys

Exchange `paygate.payments`, type `topic`, durable. Messages are persistent JSON.

| Domain event | Routing key | When |
|---|---|---|
| `PaymentInitiated` | `payment.initiated` | Payment stored, before the gateway call |
| `PaymentPending` | `payment.pending` | Gateway accepted the charge; waiting for the customer |
| `PaymentCompleted` | `payment.completed` | Customer paid |
| `PaymentFailed` | `payment.failed` | Declined, or failed at the gateway |
| `PaymentCancelled` | `payment.cancelled` | Cancelled through the API |
| `PaymentExpired` | `payment.expired` | Not paid in time |
| `RefundRequested` | `refund.requested` | Refund accepted for processing |
| `RefundCompleted` | `refund.completed` | Gateway confirmed the refund |
| `RefundFailed` | `refund.failed` | Gateway declined the refund; its amount is refundable again |

Bind `payment.*` for all payment outcomes, `refund.*` for refunds, or `#` for everything.

## Envelope

```json
{
  "event_id": "5f0c…",
  "event_type": "payment.completed",
  "aggregate_id": "9b1e…",
  "aggregate_type": "PAYMENT",
  "version": 1,
  "occurred_at": "2026-10-15T09:12:44.120Z",
  "source_service": "paygate-service",
  "data": { "payment_id": "9b1e…", "paid_at": "2026-10-15T09:12:43Z", "occurred_at": "2026-10-15T09:12:44.120Z" }
}
```

`event_id` is also the AMQP `message_id`. `aggregate_id` is the payment ID. `event_type`
is the routing key. `version` is the envelope version.

## Payloads (`data`)

Every payload has `payment_id` and `occurred_at`. The other fields:

| Event | Fields |
|---|---|
| `payment.initiated` | — |
| `payment.pending` | `gateway_transaction_id` |
| `payment.completed` | `paid_at` |
| `payment.failed` | `error_code`, `error_message` (gateway status or a PayGate code such as `CHARGE_NOT_CREATED`, `CHARGE_REPLY_LOST`) |
| `payment.cancelled` | — |
| `payment.expired` | — |
| `refund.requested` | `refund_id`, `amount` |
| `refund.completed` | `refund_id`, `amount`, `refunded_amount` (total refunded so far), `payment_status` (`PARTIALLY_REFUNDED` or `REFUNDED`) |
| `refund.failed` | `refund_id`, `reason` |

The keys are pinned by `TestEventPayloadKeys` in `internal/domain/payment/events_test.go`.
Events stored before 2026-10-15 used Go field names (`PaymentID`, `GatewayTxID`, ...) in
`payment_events.payload`; they were never consumed outside PayGate.

## Delivery Guarantees

Events go through the transactional outbox ([architecture.md](architecture.md#background-workers)):

- **At least once.** A crash between publishing and marking the row published re-sends the
  event. Deduplicate on `event_id`.
- **Not ordered.** Several workers publish concurrently, and an event whose publish failed
  is retried later while newer events go out. Don't infer state from arrival order: when
  order matters, compare `occurred_at` or read the payment with `GET /payments/{id}`.
- **Confirmed.** The publisher waits for RabbitMQ's publisher confirm before marking an
  event published.
- **Eventually given up.** After 6 failed publishes an outbox row becomes `FAILED`; watch
  `GET /api/v1/admin/outbox/stats`.

## Known Gaps

- Payloads carry only PayGate's `payment_id`, not the caller's `reference_id` or
  `caller_service`. Consumers map `payment_id` back to their own records (it is returned
  by `POST /payments`).
- Every service bound to the exchange sees every service's events; routing keys don't
  include the caller.
