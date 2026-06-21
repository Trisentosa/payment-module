package apperror

import (
	"errors"
	"fmt"
)

type Code string

const (
	CodeNotFound            Code = "NOT_FOUND"
	CodeAlreadyExists       Code = "ALREADY_EXISTS"
	CodeInvalidInput        Code = "INVALID_INPUT"
	CodeInvalidState        Code = "INVALID_STATE"
	CodeGatewayError        Code = "GATEWAY_ERROR"
	CodeInternalError       Code = "INTERNAL_ERROR"
	CodeIdempotencyConflict Code = "IDEMPOTENCY_CONFLICT"
)

type AppError struct {
	Code    Code
	Message string
	Cause   error
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error { return e.Cause }

func NotFound(msg string) *AppError      { return &AppError{Code: CodeNotFound, Message: msg} }
func AlreadyExists(msg string) *AppError { return &AppError{Code: CodeAlreadyExists, Message: msg} }
func InvalidInput(msg string) *AppError  { return &AppError{Code: CodeInvalidInput, Message: msg} }
func InvalidState(msg string) *AppError  { return &AppError{Code: CodeInvalidState, Message: msg} }
func GatewayError(msg string, cause error) *AppError {
	return &AppError{Code: CodeGatewayError, Message: msg, Cause: cause}
}
func Internal(msg string, cause error) *AppError {
	return &AppError{Code: CodeInternalError, Message: msg, Cause: cause}
}

func IsCode(err error, code Code) bool {
	var ae *AppError
	if errors.As(err, &ae) {
		return ae.Code == code
	}
	return false
}
