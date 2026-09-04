//go:build dev

package grpcapi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/apierr"
	userv1 "github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpc/gen/user/v1"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
)

// A guide pointing at the wrong method is a guide where clicking does nothing.
// This test binds the content to the constants protoc generates, and to the real authUnary.
func TestConsoleGuide(t *testing.T) {
	t.Parallel()

	g := ConsoleGuide()

	require.NotEmpty(t, g.Title)
	// The metadata field has its name prefilled as soon as the page opens, leaving only the value to paste.
	require.Equal(t, []string{"authorization: Bearer "}, g.DefaultMetadata)

	// Ordered the way the user actually has to work — sign up, get a token, and only then call the things that need one.
	want := []struct {
		full       string
		needsToken bool
	}{
		{full: userv1.AuthService_Register_FullMethodName},
		{full: userv1.AuthService_Login_FullMethodName},
		{full: userv1.UserService_CreateUser_FullMethodName, needsToken: true},
		{full: userv1.UserService_ListUsers_FullMethodName, needsToken: true},
		{full: userv1.UserService_GetUser_FullMethodName, needsToken: true},
		{full: userv1.UserService_UpdateUser_FullMethodName, needsToken: true},
		{full: userv1.UserService_DeleteUser_FullMethodName, needsToken: true},
		{full: userv1.AuthService_Refresh_FullMethodName},
	}
	require.Len(t, g.Examples, len(want))

	for i, ex := range g.Examples {
		full := "/" + ex.Service + "/" + ex.Method
		require.Equal(t, want[i].full, full)

		// An example must say the same thing about metadata as the interceptor really enforces.
		_, err := authUnary(apptest.Verifier{})(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: full},
			func(context.Context, any) (any, error) { return nil, nil })

		if !want[i].needsToken {
			require.NoError(t, err, "%s must be callable with no metadata attached", full)
			require.Empty(t, ex.Metadata, "example %s should not prefill an authorization row and mislead", full)
			continue
		}
		require.ErrorIs(t, err, apierr.ErrUnauthenticated)
		require.Equal(t, []string{"authorization: Bearer "}, ex.Metadata)
	}
}
