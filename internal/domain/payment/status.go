package payment

type Status string

const (
	StatusInitiated       Status = "INITIATED"
	StatusPending         Status = "PENDING"
	StatusProcessing      Status = "PROCESSING"
	StatusCompleted       Status = "COMPLETED"
	StatusFailed          Status = "FAILED"
	StatusCancelled       Status = "CANCELLED"
	StatusExpired         Status = "EXPIRED"
	StatusRefundRequested Status = "REFUND_REQUESTED"
	StatusRefunded        Status = "REFUNDED"
	StatusRefundFailed    Status = "REFUND_FAILED"
)

var terminalStatuses = map[Status]bool{
	StatusCompleted:    true,
	StatusFailed:       true,
	StatusExpired:      true,
	StatusRefunded:     true,
	StatusRefundFailed: true,
}

func (s Status) IsTerminal() bool { return terminalStatuses[s] }
