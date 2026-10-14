package command

import (
	"context"
	"errors"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

// recordAttempt stores one gateway call. A failure to record is logged, never returned:
// the gateway call has already happened and the command must carry on.
func recordAttempt(ctx context.Context, rec ports.AttemptRecorder, a ports.Attempt) {
	if rec == nil {
		return
	}
	if err := rec.Record(ctx, a); err != nil {
		logger.FromContext(ctx).Error("gateway attempt not recorded",
			"payment_id", a.PaymentID, "operation", a.Operation, "outcome", a.Outcome, "err", err)
	}
}

// failedAttempt fills an attempt's outcome and error fields from a gateway error.
func failedAttempt(a ports.Attempt, err error) ports.Attempt {
	a.Outcome = ports.AttemptFailed
	if errors.Is(err, ports.ErrUnknownOutcome) {
		a.Outcome = ports.AttemptUnknown
	}
	a.ErrorCode, a.ErrorMessage = "ERROR", err.Error()
	var ae *apperror.AppError
	if errors.As(err, &ae) {
		a.ErrorCode = string(ae.Code)
	}
	return a
}
