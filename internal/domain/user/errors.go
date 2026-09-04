package user

import (
	"errors"
	"fmt"
	"strings"
)

// Domain errors are declared here and nowhere else.
// Every adapter layer maps these onto the vocabulary of its own transport.
var (
	ErrNotFound           = errors.New("user not found")
	ErrEmailTaken         = errors.New("this email is already in use")
	ErrInvalidCredentials = errors.New("incorrect email or password")
	ErrUnauthorized       = errors.New("unauthorized")

	// ErrForbidden means the caller is known but is not allowed to touch the row they named —
	// deliberately distinct from ErrUnauthorized, which is about the caller not being known at all.
	ErrForbidden = errors.New("forbidden")
)

// ErrValidation says which field is wrong and why.
// It is a struct rather than a sentinel because it has to carry per-field detail up to the caller.
type ErrValidation struct {
	Field  string
	Reason string
}

func (e ErrValidation) Error() string {
	return fmt.Sprintf("invalid value in field %q: %s", e.Field, e.Reason)
}

// Is makes errors.Is(err, user.ErrValidation{}) work regardless of the field values.
func (e ErrValidation) Is(target error) bool {
	_, ok := target.(ErrValidation)
	return ok
}

// ValidationErrors is every field that failed at once, in the order they were checked, so a caller fixing a form
// learns about all of them in a single round trip rather than one per request.
//
// Unwrap returns each ErrValidation, so errors.Is(err, ErrValidation{}) and errors.As keep working on the whole —
// nothing that only cares whether *something* was invalid has to know this type exists.
type ValidationErrors []ErrValidation

func (e ValidationErrors) Error() string {
	parts := make([]string, 0, len(e))
	for _, each := range e {
		parts = append(parts, each.Error())
	}
	return strings.Join(parts, "; ")
}

func (e ValidationErrors) Unwrap() []error {
	out := make([]error, 0, len(e))
	for _, each := range e {
		out = append(out, each)
	}
	return out
}
