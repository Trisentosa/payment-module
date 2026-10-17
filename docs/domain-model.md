# Domain Model

One aggregate: **Payment** (`internal/domain/payment`). Its refunds live inside it and are
always loaded and saved with it, so the rules that span both (never refund more than was
paid) are checked in one place under one optimistic lock.

## Payment

| Field | Meaning |
|---|---|
| `ID` | PayGate's UUID. Also sent to the gateway as the order ID |
| `ReferenceID` + `CallerService` | The caller's own ID for the payment, unique per calling service. This pair is the idempotency key of `POST /payments` |
| `Amount` | `Money{Amount, Currency}`: an integer in the currency's smallest unit (IDR has none, so 150000 means Rp150.000) |
| `GatewayType`, `PaymentMethodType` | Which gateway, and how the customer pays: `VIRTUAL_ACCOUNT`, `QRIS`, `GOPAY`, `SHOPEEPAY`, `CARD` |
| `GatewayTransactionID` | The gateway's ID for the charge, once it is known |
| `Status` | See below |
| `Refunds` | Every refund ever requested, including failed ones |
| `Version` | Optimistic lock, incremented on each save |

## Payment Statuses

```
            charge accepted              customer paid
INITIATED ───────────────────▶ PENDING ───────────────────▶ COMPLETED ──refunds──▶ PARTIALLY_REFUNDED ──▶ REFUNDED
    │                            │                              └──────────refund in full──────────────────▲
    │ charge declined / failed   ├─▶ FAILED      (declined, or the gateway reports failure)
    ├─▶ FAILED                   ├─▶ CANCELLED   (DELETE /payments/{id})
    └─▶ EXPIRED                  └─▶ EXPIRED     (expiry job, admin force-expire)
```

| Status | Meaning | Left by |
|---|---|---|
| `INITIATED` | Stored, gateway outcome unknown. A payment only stays here when the charge call got no usable answer; the reconciler settles it | `MarkPending`, `MarkFailed`, `MarkExpired` |
| `PENDING` | The gateway accepted the charge; waiting for the customer | `MarkCompleted`, `MarkFailed`, `Cancel`, `MarkExpired` |
| `PROCESSING` | Reserved. Nothing sets it today; `MarkCompleted` and `Cancel` already accept it | — |
| `COMPLETED` | Paid | `CompleteRefund` |
| `PARTIALLY_REFUNDED` | Paid, part refunded | `CompleteRefund` |
| `FAILED`, `CANCELLED`, `EXPIRED`, `REFUNDED` | Final | — |

`COMPLETED` and `PARTIALLY_REFUNDED` count as terminal for payment-status notifications
(`IsTerminal`), but still accept refunds. `WasPaid()` is true for the three paid statuses.

A gateway notification that arrives after a payment closed is not applied. If it says the
customer paid a payment PayGate already closed unpaid, the callback handler logs an error
for a person to resolve (the money was taken; refund it at the gateway).

## Refunds

| Rule | Where |
|---|---|
| Only `COMPLETED` and `PARTIALLY_REFUNDED` payments can be refunded | `RequestRefund` |
| A refund `reference_id` is unique within its payment; repeating it with the same amount replays the original refund | `RequestRefund`, `command.RefundPaymentHandler` |
| A new refund may not exceed `RefundableAmount()` = paid − succeeded − pending. Pending refunds stay reserved until they settle | `RequestRefund` |
| Amount 0 in the API means "everything refundable" | `command.RefundPaymentHandler` |
| The gateway must support refunds for the method; partial refunds need `PartialRefund`. Midtrans: VA can't be refunded through the API (422), ShopeePay only in full | `command.RefundPaymentHandler` via `GatewayPort.Capability` |

Refund statuses: `PENDING` → `SUCCEEDED` or `FAILED`. The refund's ID is the gateway's
idempotency key (`refund_key` on Midtrans), so a refund re-sent after a lost reply is never
paid out twice. `CompleteRefund` on an already succeeded refund is a no-op, because gateways
confirm the same refund more than once. After a refund succeeds, the payment becomes
`PARTIALLY_REFUNDED` or, once the full amount is refunded, `REFUNDED`.

## Events

Every state change raises an event, stored with the aggregate in one transaction and
published through the outbox. Payloads and routing keys: [events.md](events.md).

| Method | Event |
|---|---|
| `New` | `PaymentInitiated` |
| `MarkPending` | `PaymentPending` |
| `MarkCompleted` | `PaymentCompleted` |
| `MarkFailed` | `PaymentFailed` |
| `Cancel` | `PaymentCancelled` |
| `MarkExpired` | `PaymentExpired` |
| `RequestRefund` | `RefundRequested` |
| `CompleteRefund` | `RefundCompleted` |
| `FailRefund` | `RefundFailed` |

## Ownership

Every payment belongs to the service whose API key created it. Callers can only read,
cancel and refund their own payments; another service's payment answers 404. Admin routes
see all payments. See [security.md](security.md).
