package domain

import "net/http"

type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func NewError(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

func Invalid(msg string) *Error {
	return NewError(http.StatusBadRequest, "validation_error", msg)
}

var (
	ErrUnauthorized       = NewError(http.StatusUnauthorized, "unauthorized", "authentication required")
	ErrInvalidCredentials = NewError(http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
	ErrForbidden          = NewError(http.StatusForbidden, "forbidden", "not enough permissions")
	ErrNotFound           = NewError(http.StatusNotFound, "not_found", "resource not found")
	ErrEmailTaken         = NewError(http.StatusConflict, "email_taken", "email already registered")
	ErrAlreadyMember      = NewError(http.StatusConflict, "already_member", "user is already a team member")
	ErrVersionConflict    = NewError(http.StatusConflict, "version_conflict", "task was modified concurrently, reload and retry")
	ErrTooManyRequests    = NewError(http.StatusTooManyRequests, "rate_limited", "too many requests")
)
