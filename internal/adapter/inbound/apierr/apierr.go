// Package apierr is the one vocabulary every inbound adapter uses to report a failure.
//
// It turns an error flowing up from the core into a transport-neutral Problem — a stable code, a message safe to show,
// and the per-field detail of a validation failure. Each transport then maps the code onto its own status
// (HTTP status, gRPC code) and nothing else, so REST and gRPC can never drift apart in what they tell the caller.
package apierr

import (
	"errors"

	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// Code is the stable, machine-readable identifier of a failure — the same string on every transport.
type Code string

const (
	CodeValidation         Code = "VALIDATION_ERROR"
	CodeUserNotFound       Code = "USER_NOT_FOUND"
	CodeEmailTaken         Code = "EMAIL_TAKEN"
	CodeInvalidCredentials Code = "INVALID_CREDENTIALS" //nolint:gosec // an error code, not a credential
	CodeUnauthorized       Code = "UNAUTHORIZED"
	CodeForbidden          Code = "FORBIDDEN"
	CodeInternal           Code = "INTERNAL"
)

// ErrUnauthenticated is the transport layer's own error for "no usable bearer token was presented".
// The domain's ErrUnauthorized is about a refresh token that cannot be honored; this one is about the request itself.
// Both end up as CodeUnauthorized, and neither says what exactly was wrong — that detail would only help an attacker.
var ErrUnauthenticated = errors.New("a valid bearer token is required")

// FieldIssue names one field that failed validation and why.
type FieldIssue struct {
	Field string
	Issue string
}

// Problem is what a transport needs in order to answer: which failure, what to say, and which fields were at fault.
type Problem struct {
	Code    Code
	Message string
	Fields  []FieldIssue
}

// Classify is pure and total — every error maps to exactly one Problem, and anything unrecognized is CodeInternal
// with a message that reveals nothing. Logging the underlying error is the caller's job.
func Classify(err error) Problem {
	var (
		invalid  user.ErrValidation
		invalids user.ValidationErrors
	)

	switch {
	// The many-field form first: errors.As on the single form would also match it, but only report its first field.
	case errors.As(err, &invalids):
		fields := make([]FieldIssue, 0, len(invalids))
		for _, v := range invalids {
			fields = append(fields, FieldIssue{Field: v.Field, Issue: v.Reason})
		}
		return Problem{Code: CodeValidation, Message: "the data sent is invalid", Fields: fields}
	case errors.As(err, &invalid):
		return Problem{
			Code:    CodeValidation,
			Message: "the data sent is invalid",
			Fields:  []FieldIssue{{Field: invalid.Field, Issue: invalid.Reason}},
		}
	case errors.Is(err, user.ErrNotFound):
		return Problem{Code: CodeUserNotFound, Message: "no user matches the one specified"}
	case errors.Is(err, user.ErrEmailTaken):
		return Problem{Code: CodeEmailTaken, Message: "this email is already in use"}
	case errors.Is(err, user.ErrInvalidCredentials):
		// Deliberately distinct from CodeUnauthorized: this is "the login failed",
		// not "something needing a token was called without one".
		return Problem{Code: CodeInvalidCredentials, Message: "incorrect email or password"}
	case errors.Is(err, ErrUnauthenticated), errors.Is(err, user.ErrUnauthorized):
		return Problem{Code: CodeUnauthorized, Message: "authentication is required before calling this"}
	case errors.Is(err, user.ErrForbidden):
		// The caller is known; the row is not theirs. Saying so reveals nothing about whether the row exists.
		return Problem{Code: CodeForbidden, Message: "you may only change your own account"}
	default:
		return Problem{Code: CodeInternal, Message: "an internal error occurred"}
	}
}
