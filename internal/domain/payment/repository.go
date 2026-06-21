package payment

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	Save(ctx context.Context, p *Payment) error
	FindByID(ctx context.Context, id uuid.UUID) (*Payment, error)
	FindByReference(ctx context.Context, referenceID, callerService string) (*Payment, error)
	FindByGatewayTransactionID(ctx context.Context, gatewayTxID string) (*Payment, error)
	FindExpired(ctx context.Context, limit int) ([]*Payment, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status Status, fields map[string]any) error
}
