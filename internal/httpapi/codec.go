package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return platform.FieldError{Field: "body", Message: err.Error()}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return platform.FieldError{Field: "body", Message: "request must contain one JSON object"}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	switch {
	case errors.Is(err, platform.ErrUnauthorized):
		status, code = http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, platform.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, platform.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, platform.ErrConflict), errors.Is(err, platform.ErrLeaseLost), errors.Is(err, platform.ErrBudgetExceeded):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, platform.ErrInvalidState):
		status, code = http.StatusUnprocessableEntity, "invalid_state"
	case errors.Is(err, platform.ErrValidation):
		status, code = http.StatusBadRequest, "validation_failed"
	case errors.Is(err, r.Context().Err()):
		status, code = 499, "request_cancelled"
	}
	message := err.Error()
	if status == http.StatusInternalServerError {
		message = "an internal error occurred"
	}
	writeJSON(w, status, errorResponse{Error: errorBody{Code: code, Message: message, RequestID: requestIDFrom(r.Context())}})
}
