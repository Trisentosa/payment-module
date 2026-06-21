package middleware

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

var codeToHTTP = map[apperror.Code]int{
	apperror.CodeNotFound:            http.StatusNotFound,
	apperror.CodeAlreadyExists:       http.StatusConflict,
	apperror.CodeInvalidInput:        http.StatusBadRequest,
	apperror.CodeInvalidState:        http.StatusUnprocessableEntity,
	apperror.CodeGatewayError:        http.StatusBadGateway,
	apperror.CodeIdempotencyConflict: http.StatusConflict,
	apperror.CodeInternalError:       http.StatusInternalServerError,
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func WriteError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	resp := errorResponse{Code: string(apperror.CodeInternalError), Message: "internal server error"}

	var ae *apperror.AppError
	if errors.As(err, &ae) {
		if s, ok := codeToHTTP[ae.Code]; ok {
			status = s
		}
		resp.Code = string(ae.Code)
		resp.Message = ae.Message
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}
