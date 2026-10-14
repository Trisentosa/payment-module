# ADR-001: Call the gateway synchronously, reconcile unknown outcomes

**Status:** Accepted (2026-10)

## Context

`POST /payments` must hand the caller something to show the customer: a VA number, a QR
code, a redirect to 3-D Secure or a wallet. Only the gateway can produce these, so the
gateway's charge reply is needed before PayGate can answer.

The alternative is asynchronous: store the payment, answer 202, call the gateway from a
worker, and make the caller poll or wait for an event to get the instructions.

Either way, a gateway call can end with no usable answer (timeout, dropped connection,
5xx), and the charge or refund may or may not have happened.

## Decision

Charge, cancel and refund calls are made synchronously inside the request.

When a call ends without a usable answer, the adapter returns an error wrapping
`ports.ErrUnknownOutcome` and nothing is guessed:

- an unknown charge leaves the payment `INITIATED` and answers 502 with the payment ID;
- an unknown refund stays `PENDING` with its amount reserved and answers 202.

A reconciler asks the gateway for the transaction's status later and settles both. Refunds
use the refund ID as the gateway's idempotency key, so re-sending one is safe.

## Consequences

- Callers get payment instructions in one round trip, which most checkout flows need.
- Request latency includes the gateway's (up to `MIDTRANS_TIMEOUT_MS`, 30 s by default).
- A charge whose reply was lost can't be shown to the customer, so the reconciler cancels it
  at the gateway rather than leaving it payable. The caller retries with a new `reference_id`.
- Every gateway call is recorded in `payment_attempts`, including unknown outcomes, so a
  stuck payment can be traced.
