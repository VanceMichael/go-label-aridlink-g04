package platform

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound       = errors.New("resource not found")
	ErrConflict       = errors.New("resource conflict")
	ErrForbidden      = errors.New("operation forbidden")
	ErrUnauthorized   = errors.New("authentication required")
	ErrInvalidState   = errors.New("invalid state transition")
	ErrValidation     = errors.New("validation failed")
	ErrLeaseLost      = errors.New("lease ownership lost")
	ErrBudgetExceeded = errors.New("budget exceeded")
)

type FieldError struct {
	Field   string
	Message string
}

func (e FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func (e FieldError) Unwrap() error { return ErrValidation }

type StateError struct {
	Resource string
	Current  string
	Target   string
}

func (e StateError) Error() string {
	return fmt.Sprintf("%s cannot transition from %s to %s", e.Resource, e.Current, e.Target)
}

func (e StateError) Unwrap() error { return ErrInvalidState }

type ConflictError struct {
	Resource string
	Key      string
}

func (e ConflictError) Error() string {
	return fmt.Sprintf("%s conflicts on %s", e.Resource, e.Key)
}

func (e ConflictError) Unwrap() error { return ErrConflict }
