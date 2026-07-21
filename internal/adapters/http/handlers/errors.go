package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteDomainError maps stable domain errors to the shared API envelope and
// avoids leaking adapter details for unexpected failures.
func WriteDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrValidation):
		WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, domain.ErrForbidden):
		WriteError(w, http.StatusForbidden, "forbidden", "repository source is not allowed")
	case errors.Is(err, domain.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, domain.ErrConflict):
		WriteError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, domain.ErrInvalidStatus):
		WriteError(w, http.StatusConflict, "invalid_status", err.Error())
	case errors.Is(err, domain.ErrApprovalNeeded):
		WriteError(w, http.StatusConflict, "approval_required", "onboarding approval is required")
	case errors.Is(err, domain.ErrWriteScope):
		WriteError(w, http.StatusForbidden, "write_scope_violation", "operation exceeds the approved write scope")
	default:
		WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// WriteError renders the common API error envelope.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: ErrorBody{Code: code, Message: message}})
}
