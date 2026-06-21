package payment

import (
	"time"

	"github.com/google/uuid"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type GatewayType string

const (
	GatewayMidtrans GatewayType = "MIDTRANS"
	GatewayDoku     GatewayType = "DOKU"
	GatewayStripe   GatewayType = "STRIPE"
)

type CustomerInfo struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
}

type Payment struct {
	ID                      uuid.UUID
	ReferenceID             string
	CallerService           string
	GatewayType             GatewayType
	GatewayTransactionID    string
	Status                  Status
	Amount                  Money
	PaymentMethodType       string
	CustomerInfo            CustomerInfo
	Metadata                map[string]any
	GatewayRequestPayload   map[string]any
	GatewayResponsePayload  map[string]any
	Description             string
	ExpiredAt               *time.Time
	PaidAt                  *time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
	DeletedAt               *time.Time

	events []DomainEvent
}

// New creates a Payment in INITIATED state. Does not call the gateway.
func New(
	referenceID, callerService string,
	amount Money,
	gatewayType GatewayType,
	methodType string,
	customer CustomerInfo,
	description string,
	expiredAt *time.Time,
	metadata map[string]any,
) (*Payment, error) {
	if referenceID == "" {
		return nil, apperror.InvalidInput("reference_id is required")
	}
	if callerService == "" {
		return nil, apperror.InvalidInput("caller_service is required")
	}

	now := time.Now().UTC()
	p := &Payment{
		ID:                uuid.New(),
		ReferenceID:       referenceID,
		CallerService:     callerService,
		GatewayType:       gatewayType,
		Status:            StatusInitiated,
		Amount:            amount,
		PaymentMethodType: methodType,
		CustomerInfo:      customer,
		Description:       description,
		ExpiredAt:         expiredAt,
		Metadata:          metadata,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	p.raise(PaymentInitiated{PaymentID: p.ID, OccurredAt: now})
	return p, nil
}

// MarkPending transitions to PENDING after the gateway acknowledges.
func (p *Payment) MarkPending(gatewayTxID string, gatewayResp map[string]any) error {
	if p.Status != StatusInitiated {
		return apperror.InvalidState("can only mark PENDING from INITIATED, current: " + string(p.Status))
	}
	now := time.Now().UTC()
	p.GatewayTransactionID = gatewayTxID
	p.GatewayResponsePayload = gatewayResp
	p.Status = StatusPending
	p.UpdatedAt = now
	p.raise(PaymentPending{PaymentID: p.ID, GatewayTxID: gatewayTxID, OccurredAt: now})
	return nil
}

// PopEvents returns and clears the uncommitted event list.
func (p *Payment) PopEvents() []DomainEvent {
	evts := p.events
	p.events = nil
	return evts
}

func (p *Payment) raise(e DomainEvent) { p.events = append(p.events, e) }
