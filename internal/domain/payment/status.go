package payment

type Status string

const (
	StatusInitiated         Status = "INITIATED"
	StatusPending           Status = "PENDING"
	StatusProcessing        Status = "PROCESSING"
	StatusCompleted         Status = "COMPLETED"
	StatusFailed            Status = "FAILED"
	StatusCancelled         Status = "CANCELLED"
	StatusExpired           Status = "EXPIRED"
	StatusPartiallyRefunded Status = "PARTIALLY_REFUNDED"
	StatusRefunded          Status = "REFUNDED"
)

// terminalStatuses are the statuses a gateway's payment-status notification can no longer
// change. Refund notifications are applied separately, so COMPLETED counts as terminal here.
var terminalStatuses = map[Status]bool{
	StatusCompleted:         true,
	StatusFailed:            true,
	StatusCancelled:         true,
	StatusExpired:           true,
	StatusPartiallyRefunded: true,
	StatusRefunded:          true,
}

func (s Status) IsTerminal() bool { return terminalStatuses[s] }

// WasPaid reports whether the customer's money was collected, even if since refunded.
func (s Status) WasPaid() bool {
	return s == StatusCompleted || s == StatusPartiallyRefunded || s == StatusRefunded
}
