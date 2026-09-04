package apierr_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/apierr"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want apierr.Code
	}{
		{name: "validation", err: user.ErrValidation{Field: "email", Reason: "wrong"}, want: apierr.CodeValidation},
		{name: "wrapped validation", err: fmt.Errorf("outer: %w", user.ErrValidation{Field: "id", Reason: "x"}), want: apierr.CodeValidation},
		{name: "user not found", err: user.ErrNotFound, want: apierr.CodeUserNotFound},
		{name: "email taken", err: user.ErrEmailTaken, want: apierr.CodeEmailTaken},
		{name: "bad login", err: user.ErrInvalidCredentials, want: apierr.CodeInvalidCredentials},
		{name: "no token on the request", err: apierr.ErrUnauthenticated, want: apierr.CodeUnauthorized},
		{name: "refresh token cannot be honored", err: user.ErrUnauthorized, want: apierr.CodeUnauthorized},
		{name: "someone else's row", err: user.ErrForbidden, want: apierr.CodeForbidden},
		{name: "several fields at once", err: user.ValidationErrors{{Field: "name", Reason: "x"}, {Field: "email", Reason: "y"}}, want: apierr.CodeValidation},
		{name: "anything else", err: context.DeadlineExceeded, want: apierr.CodeInternal},
		{name: "nil", err: nil, want: apierr.CodeInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := apierr.Classify(tt.err)

			require.Equal(t, tt.want, got.Code)
			require.NotEmpty(t, got.Message)
		})
	}
}

func TestClassify_ValidationCarriesTheField(t *testing.T) {
	t.Parallel()

	got := apierr.Classify(user.ErrValidation{Field: "limit", Reason: "must not exceed 100"})

	require.Equal(t, []apierr.FieldIssue{{Field: "limit", Issue: "must not exceed 100"}}, got.Fields)
}

// Every field the domain rejected is reported, in the domain's order, even when the list arrives wrapped.
func TestClassify_ValidationCarriesEveryField(t *testing.T) {
	t.Parallel()

	got := apierr.Classify(fmt.Errorf("outer: %w", user.ValidationErrors{
		{Field: "name", Reason: "must be 1–100 characters"},
		{Field: "email", Reason: "invalid email format"},
		{Field: "password", Reason: "must be at least 8 characters"},
	}))

	require.Equal(t, apierr.CodeValidation, got.Code)
	require.Equal(t, []apierr.FieldIssue{
		{Field: "name", Issue: "must be 1–100 characters"},
		{Field: "email", Issue: "invalid email format"},
		{Field: "password", Issue: "must be at least 8 characters"},
	}, got.Fields)
}

// The message for an unrecognized error must never echo the error itself — that is where internal structure leaks.
func TestClassify_InternalHidesDetail(t *testing.T) {
	t.Parallel()

	got := apierr.Classify(errors.New("mongo: connection refused to collection users"))

	require.Equal(t, apierr.CodeInternal, got.Code)
	require.NotContains(t, got.Message, "mongo")
	require.Empty(t, got.Fields)
}
