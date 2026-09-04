package grpcapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/actor"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/apierr"
	userv1 "github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpc/gen/user/v1"
	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

var getUserInfo = &grpc.UnaryServerInfo{FullMethod: userv1.UserService_GetUser_FullMethodName}

func ptr[T any](v T) *T { return &v }

func TestUserService_CreateUser(t *testing.T) {
	t.Parallel()

	uc := &apptest.FakeUsers{User: apptest.SeededUser()}
	svc := &userService{users: uc}

	resp, err := svc.CreateUser(context.Background(), &userv1.CreateUserRequest{
		Name: "Natthawat N.", Email: "Natthawat@Example.com", Password: apptest.SeededPassword,
	})

	require.NoError(t, err)
	require.Equal(t, apptest.SeededID, resp.GetUser().GetId())
	require.Equal(t, "natthawat@example.com", resp.GetUser().GetEmail())
	require.Equal(t, apptest.SeededAt, resp.GetUser().GetCreatedAt().AsTime())
	// The input is forwarded to the very same use case REST calls, so no business rule is restated.
	require.Equal(t, "Natthawat@Example.com", uc.LastCreate.Email)
}

// The two transports must describe one row identically — REST prints whole seconds, so this side sends whole seconds.
func TestToProto_TimestampsAreWholeSeconds(t *testing.T) {
	t.Parallel()

	u := apptest.SeededUser()
	u.CreatedAt = apptest.SeededAt.Add(750 * time.Millisecond)

	require.Equal(t, apptest.SeededAt, toProto(u).GetCreatedAt().AsTime())
}

func TestUserService_GetUser(t *testing.T) {
	t.Parallel()

	t.Run("returns the user", func(t *testing.T) {
		t.Parallel()
		uc := &apptest.FakeUsers{User: apptest.SeededUser()}
		svc := &userService{users: uc}

		resp, err := svc.GetUser(context.Background(), &userv1.GetUserRequest{Id: apptest.SeededID})

		require.NoError(t, err)
		require.Equal(t, "Natthawat N.", resp.GetUser().GetName())
		require.Equal(t, apptest.SeededID, uc.LastID)
		require.NotContains(t, resp.String(), "hash", "no password field exists in the contract")
	})

	t.Run("a miss and a malformed id are passed on for the interceptor to translate", func(t *testing.T) {
		t.Parallel()
		for _, refusal := range []error{user.ErrNotFound, user.ErrValidation{Field: "id", Reason: "invalid format"}} {
			svc := &userService{users: &apptest.FakeUsers{Err: refusal}}

			_, err := svc.GetUser(context.Background(), &userv1.GetUserRequest{Id: "x"})

			require.ErrorIs(t, err, refusal)
		}
	})
}

func TestUserService_ListUsers(t *testing.T) {
	t.Parallel()

	t.Run("meta must report the limit actually used and whether a next page exists", func(t *testing.T) {
		t.Parallel()

		uc := &apptest.FakeUsers{Users: []user.User{*apptest.SeededUser()}, Next: "cursor-next-page"}
		svc := &userService{users: uc}

		resp, err := svc.ListUsers(context.Background(), &userv1.ListUsersRequest{
			Limit: proto.Int32(2), Cursor: "cursor-current", Query: "natthawat",
		})

		require.NoError(t, err)
		require.Len(t, resp.GetUsers(), 1)
		require.Equal(t, "natthawat@example.com", resp.GetUsers()[0].GetEmail())
		require.Equal(t, int32(2), resp.GetMeta().GetLimit())
		require.True(t, resp.GetMeta().GetHasMore())
		require.Equal(t, "cursor-next-page", resp.GetMeta().GetNextCursor())
		require.Equal(t, app.ListFilter{Limit: ptr(2), Cursor: "cursor-current", Query: "natthawat"}, uc.LastFilter)
	})

	t.Run("the last page must carry no next_cursor", func(t *testing.T) {
		t.Parallel()

		svc := &userService{users: &apptest.FakeUsers{Users: []user.User{*apptest.SeededUser()}}}

		resp, err := svc.ListUsers(context.Background(), &userv1.ListUsersRequest{})

		require.NoError(t, err)
		require.False(t, resp.GetMeta().GetHasMore())
		require.Nil(t, resp.GetMeta().NextCursor)
	})

	t.Run("sending no limit is forwarded as nil, for the use case to fill in the default", func(t *testing.T) {
		t.Parallel()

		uc := &apptest.FakeUsers{}
		svc := &userService{users: uc}

		resp, err := svc.ListUsers(context.Background(), &userv1.ListUsersRequest{})

		require.NoError(t, err)
		require.Nil(t, uc.LastFilter.Limit)
		require.Equal(t, int32(app.DefaultListLimit), resp.GetMeta().GetLimit(), "the meta echoes what the use case applied")
	})

	// The range check is the use case's rule; this side forwards the value as sent and passes the refusal on untouched.
	t.Run("an unusable limit is forwarded as sent and the refusal passed on", func(t *testing.T) {
		t.Parallel()

		refusal := user.ErrValidation{Field: "limit", Reason: "must not exceed 100"}
		uc := &apptest.FakeUsers{Err: refusal}
		svc := &userService{users: uc}

		_, err := svc.ListUsers(context.Background(), &userv1.ListUsersRequest{Limit: proto.Int32(1000)})

		require.Equal(t, 1000, *uc.LastFilter.Limit)
		require.ErrorIs(t, err, refusal)
	})
}

// A field that was not sent must be left untouched — it is optional in the contract to tell it apart from "sent as an empty value".
func TestUserService_UpdateUser(t *testing.T) {
	t.Parallel()

	uc := &apptest.FakeUsers{User: apptest.SeededUser()}
	svc := &userService{users: uc}

	resp, err := svc.UpdateUser(actor.Set(context.Background(), apptest.SeededID), &userv1.UpdateUserRequest{
		Id: apptest.SeededID, Name: proto.String("New Name"),
	})

	require.NoError(t, err)
	require.Equal(t, "Natthawat N.", resp.GetUser().GetName())
	require.Equal(t, "New Name", *uc.LastUpdate.Name)
	require.Nil(t, uc.LastUpdate.Email, "an email that was not sent must be left untouched")
	require.Equal(t, apptest.SeededID, uc.LastActor, "the actor authUnary set must reach the use case")
}

func TestUserService_DeleteUser(t *testing.T) {
	t.Parallel()

	t.Run("a successful delete answers with an empty message", func(t *testing.T) {
		t.Parallel()

		uc := &apptest.FakeUsers{}
		svc := &userService{users: uc}

		resp, err := svc.DeleteUser(actor.Set(context.Background(), apptest.SeededID), &userv1.DeleteUserRequest{Id: apptest.SeededID})

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, apptest.SeededID, uc.LastID)
		require.Equal(t, apptest.SeededID, uc.LastActor)
	})

	t.Run("someone else's row is passed on as ErrForbidden for the interceptor to translate", func(t *testing.T) {
		t.Parallel()

		svc := &userService{users: &apptest.FakeUsers{Err: user.ErrForbidden}}

		_, err := svc.DeleteUser(actor.Set(context.Background(), apptest.SeededID), &userv1.DeleteUserRequest{Id: "someone-else"})

		require.ErrorIs(t, err, user.ErrForbidden)
	})

	t.Run("a repeated delete passes ErrNotFound on for the interceptor to translate", func(t *testing.T) {
		t.Parallel()

		svc := &userService{users: &apptest.FakeUsers{Err: user.ErrNotFound}}

		_, err := svc.DeleteUser(context.Background(), &userv1.DeleteUserRequest{Id: "x"})

		require.ErrorIs(t, err, user.ErrNotFound)
	})
}

// One status table per transport — errorsUnary is where the shared vocabulary becomes gRPC codes, once, for every method.
func TestErrorsUnary(t *testing.T) {
	t.Parallel()

	failWith := func(err error) grpc.UnaryHandler {
		return func(context.Context, any) (any, error) { return nil, err }
	}

	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{name: "user not found", err: user.ErrNotFound, want: codes.NotFound},
		{name: "duplicate email", err: user.ErrEmailTaken, want: codes.AlreadyExists},
		{name: "invalid data", err: user.ErrValidation{Field: "email", Reason: "wrong"}, want: codes.InvalidArgument},
		{name: "bad login", err: user.ErrInvalidCredentials, want: codes.Unauthenticated},
		{name: "no token on the request", err: apierr.ErrUnauthenticated, want: codes.Unauthenticated},
		{name: "refresh token cannot be honored", err: user.ErrUnauthorized, want: codes.Unauthenticated},
		{name: "someone else's row", err: user.ErrForbidden, want: codes.PermissionDenied},
		{name: "the deadline ran out", err: fmt.Errorf("find user: %w", context.DeadlineExceeded), want: codes.DeadlineExceeded},
		{name: "the caller went away", err: context.Canceled, want: codes.Canceled},
		{name: "an unrecognized error", err: errInternal("mongo: connection refused"), want: codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := errorsUnary(apptest.DiscardLogger())(context.Background(), nil, getUserInfo, failWith(tt.err))

			require.Equal(t, tt.want, status.Code(err))
		})
	}

	// A gRPC code is coarser than the shared vocabulary (two codes both become Unauthenticated),
	// so the stable code travels as ErrorInfo.reason — the same string REST puts in error.code — with the request id beside it.
	t.Run("every failure carries the shared code and the request id as ErrorInfo", func(t *testing.T) {
		t.Parallel()
		ctx := context.WithValue(context.Background(), requestIDKey{}, "rid-123")

		_, err := errorsUnary(apptest.DiscardLogger())(ctx, nil, getUserInfo, failWith(user.ErrInvalidCredentials))

		st := status.Convert(err)
		require.Equal(t, codes.Unauthenticated, st.Code())
		require.Len(t, st.Details(), 1)
		info, ok := st.Details()[0].(*errdetails.ErrorInfo)
		require.True(t, ok)
		require.Equal(t, string(apierr.CodeInvalidCredentials), info.GetReason())
		require.Equal(t, errorDomain, info.GetDomain())
		require.Equal(t, "rid-123", info.GetMetadata()["request_id"])
	})

	t.Run("a validation failure carries the field by machine, like details[] on the REST side", func(t *testing.T) {
		t.Parallel()

		_, err := errorsUnary(apptest.DiscardLogger())(context.Background(), nil, getUserInfo,
			failWith(user.ErrValidation{Field: "limit", Reason: "must not exceed 100"}))

		st := status.Convert(err)
		require.Equal(t, codes.InvalidArgument, st.Code())
		require.Equal(t, "the data sent is invalid", st.Message(), "the same words REST uses; the field is in the details")

		require.Len(t, st.Details(), 2, "ErrorInfo and BadRequest")
		br, ok := st.Details()[1].(*errdetails.BadRequest)
		require.True(t, ok)
		require.Len(t, br.GetFieldViolations(), 1)
		require.Equal(t, "limit", br.GetFieldViolations()[0].GetField())
		require.Equal(t, "must not exceed 100", br.GetFieldViolations()[0].GetDescription())
	})

	t.Run("a validation failure on several fields carries every one of them, in the domain's order", func(t *testing.T) {
		t.Parallel()

		_, err := errorsUnary(apptest.DiscardLogger())(context.Background(), nil, getUserInfo,
			failWith(user.ValidationErrors{{Field: "name", Reason: "empty"}, {Field: "email", Reason: "bad"}}))

		st := status.Convert(err)
		require.Equal(t, codes.InvalidArgument, st.Code())
		br, ok := st.Details()[1].(*errdetails.BadRequest)
		require.True(t, ok)
		require.Len(t, br.GetFieldViolations(), 2)
		require.Equal(t, "name", br.GetFieldViolations()[0].GetField())
		require.Equal(t, "email", br.GetFieldViolations()[1].GetField())
	})

	// An unrecognized error must not carry internal details back out to the caller.
	t.Run("an internal error hides its detail", func(t *testing.T) {
		t.Parallel()

		_, err := errorsUnary(apptest.DiscardLogger())(context.Background(), nil, getUserInfo,
			failWith(errInternal("mongo: connection refused to collection users")))

		require.Equal(t, codes.Internal, status.Code(err))
		require.NotContains(t, status.Convert(err).Message(), "collection users")
	})

	t.Run("something already spoken as a status passes through untouched", func(t *testing.T) {
		t.Parallel()
		already := status.Error(codes.DeadlineExceeded, "took too long")

		_, err := errorsUnary(apptest.DiscardLogger())(context.Background(), nil, getUserInfo, failWith(already))

		require.Same(t, already, err)
	})

	t.Run("a success passes through untouched", func(t *testing.T) {
		t.Parallel()

		got, err := errorsUnary(apptest.DiscardLogger())(context.Background(), nil, getUserInfo,
			func(context.Context, any) (any, error) { return "passed", nil })

		require.NoError(t, err)
		require.Equal(t, "passed", got)
	})
}

type errInternal string

func (e errInternal) Error() string { return string(e) }

func TestAuthUnary(t *testing.T) {
	t.Parallel()

	handler := func(context.Context, any) (any, error) { return "passed", nil }

	t.Run("passes when the token is valid", func(t *testing.T) {
		t.Parallel()
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs(authMetadataKey, "Bearer valid-token"))

		got, err := authUnary(apptest.Verifier{})(ctx, nil, getUserInfo, handler)

		require.NoError(t, err)
		require.Equal(t, "passed", got)
	})

	t.Run("passes the verified subject to the handler as the actor", func(t *testing.T) {
		t.Parallel()
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs(authMetadataKey, "Bearer valid-token"))

		var seen string
		_, err := authUnary(apptest.Verifier{})(ctx, nil, getUserInfo,
			func(ctx context.Context, _ any) (any, error) { seen = actor.ID(ctx); return nil, nil })

		require.NoError(t, err)
		require.Equal(t, apptest.SeededID, seen)
	})

	// The same rules as the REST side, because it uses the very same bearer.Token — the scheme name is case-insensitive.
	t.Run("a lowercase scheme passes too", func(t *testing.T) {
		t.Parallel()
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs(authMetadataKey, "bearer valid-token"))

		_, err := authUnary(apptest.Verifier{})(ctx, nil, getUserInfo, handler)

		require.NoError(t, err)
	})

	// Every way of failing must answer identically — which stage failed is not the caller's business.
	failures := []struct {
		name     string
		ctx      context.Context
		verifier apptest.Verifier
	}{
		{name: "no metadata", ctx: context.Background()},
		{name: "no authorization row", ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs("other", "x"))},
		{name: "no bearer prefix", ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(authMetadataKey, "valid-token"))},
		{
			name:     "an unusable token",
			ctx:      metadata.NewIncomingContext(context.Background(), metadata.Pairs(authMetadataKey, "Bearer expired")),
			verifier: apptest.Verifier{Err: user.ErrUnauthorized},
		},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := authUnary(tt.verifier)(tt.ctx, nil, getUserInfo, handler)

			require.ErrorIs(t, err, apierr.ErrUnauthenticated)
		})
	}

	// A health check must be callable without a token, otherwise the orchestrator cannot check our health.
	t.Run("a health check needs no authentication", func(t *testing.T) {
		t.Parallel()
		healthInfo := &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}
		called := false

		_, err := authUnary(apptest.Verifier{})(context.Background(), nil, healthInfo,
			func(context.Context, any) (any, error) { called = true; return nil, nil })

		require.NoError(t, err)
		require.True(t, called)
	})
}

// AuthService is where a token comes from, so it cannot be made to require one before it is called —
// the same reason /api/v1/auth/* on the REST side sits outside Authenticate. Every other RPC must require one.
// Reading the generated descriptors means a new RPC cannot be added without this test taking a position on it.
func TestPublicPrefixes_MatchTheContract(t *testing.T) {
	t.Parallel()

	for _, m := range userv1.AuthService_ServiceDesc.Methods {
		full := "/" + userv1.AuthService_ServiceDesc.ServiceName + "/" + m.MethodName
		require.True(t, isPublicMethod(full), "%s must be callable without a token", full)
	}
	for _, m := range userv1.UserService_ServiceDesc.Methods {
		full := "/" + userv1.UserService_ServiceDesc.ServiceName + "/" + m.MethodName
		require.False(t, isPublicMethod(full), "%s must require a token", full)
	}
}

func TestRecoveryUnary(t *testing.T) {
	t.Parallel()

	_, err := recoveryUnary(apptest.DiscardLogger())(context.Background(), nil, getUserInfo,
		func(context.Context, any) (any, error) { panic("boom") })

	require.Equal(t, codes.Internal, status.Code(err))
	require.NotContains(t, status.Convert(err).Message(), "boom")
}

// The server bound is a ceiling: a caller's shorter deadline is honored, a longer one is capped, none at all is given one.
func TestTimeoutUnary(t *testing.T) {
	t.Parallel()

	const bound = time.Second

	tests := []struct {
		name   string
		client time.Duration // 0 means the caller set no deadline
		want   time.Duration // the deadline the handler must see, roughly
	}{
		{name: "no deadline from the caller gets the server bound", client: 0, want: bound},
		{name: "a longer deadline from the caller is capped at the server bound", client: time.Hour, want: bound},
		{name: "a shorter deadline from the caller is kept", client: 100 * time.Millisecond, want: 100 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			if tt.client > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.client)
				defer cancel()
			}
			start := time.Now()

			_, err := timeoutUnary(bound)(ctx, nil, getUserInfo,
				func(ctx context.Context, _ any) (any, error) {
					deadline, ok := ctx.Deadline()
					require.True(t, ok, "every RPC must end up with a deadline")
					require.WithinDuration(t, start.Add(tt.want), deadline, 50*time.Millisecond)
					return nil, nil
				})

			require.NoError(t, err)
		})
	}
}

// The request line is written by loggingUnary, which runs before authUnary sets the actor on an inner context —
// the slot it reserves is what carries the id back out.
func TestLoggingUnary_PrintsTheActor(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(authMetadataKey, "Bearer valid-token"))

	_, err := loggingUnary(log)(ctx, nil, getUserInfo, func(ctx context.Context, req any) (any, error) {
		return authUnary(apptest.Verifier{})(ctx, req, getUserInfo, func(context.Context, any) (any, error) { return nil, nil })
	})

	require.NoError(t, err)
	var entry map[string]any
	require.NoError(t, json.Unmarshal(logs.Bytes(), &entry))
	require.Equal(t, "grpc_request", entry["msg"])
	require.Equal(t, apptest.SeededID, entry["actor_id"])
}

// A health server handed in from outside is the one the composition root flips to NOT_SERVING before draining.
func TestNewServer_UsesTheInjectedHealthServer(t *testing.T) {
	t.Parallel()

	hs := health.NewServer()
	srv := NewServer(Deps{
		Users: &apptest.FakeUsers{}, Auth: &apptest.FakeAuth{}, Tokens: apptest.Verifier{},
		Logger: apptest.DiscardLogger(), RPCTimeout: time.Second, Health: hs,
	})
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := healthv1.NewHealthClient(conn)

	resp, err := client.Check(context.Background(), &healthv1.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, healthv1.HealthCheckResponse_SERVING, resp.GetStatus())

	hs.Shutdown()

	resp, err = client.Check(context.Background(), &healthv1.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, healthv1.HealthCheckResponse_NOT_SERVING, resp.GetStatus(), "flipped, while still answering — the listener is not closed yet")
}

// The request id the access log prints must be the one an error log quotes, otherwise the two cannot be joined.
func TestLoggingUnary_SettlesTheRequestID(t *testing.T) {
	t.Parallel()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-id", "rid-from-client"))

	_, err := loggingUnary(apptest.DiscardLogger())(ctx, nil, getUserInfo,
		func(ctx context.Context, _ any) (any, error) {
			require.Equal(t, "rid-from-client", requestID(ctx))
			return nil, nil
		})

	require.NoError(t, err)
}

func TestNewServer_RefusesIncompleteDeps(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t, "grpcapi: Deps is missing Users, Auth, Tokens, RPCTimeout", func() { NewServer(Deps{}) })
}

func TestAuthService_Register(t *testing.T) {
	t.Parallel()

	t.Run("signing up must return the user, with no token attached", func(t *testing.T) {
		t.Parallel()

		uc := &apptest.FakeUsers{User: apptest.SeededUser()}
		svc := &authService{users: uc}

		resp, err := svc.Register(context.Background(), &userv1.RegisterRequest{
			Name: "Natthawat N.", Email: "Natthawat@Example.com", Password: apptest.SeededPassword,
		})

		require.NoError(t, err)
		require.Equal(t, apptest.SeededID, resp.GetUser().GetId())
		require.Equal(t, "natthawat@example.com", resp.GetUser().GetEmail())
		// Forwarded to the very same use case REST calls, so hashing and the duplicate-email rule both happen in their usual place.
		require.Equal(t, "Natthawat@Example.com", uc.LastCreate.Email)
	})

	t.Run("a duplicate email is passed on for the interceptor to translate", func(t *testing.T) {
		t.Parallel()

		svc := &authService{users: &apptest.FakeUsers{Err: user.ErrEmailTaken}}

		_, err := svc.Register(context.Background(), &userv1.RegisterRequest{Email: "a@b.co"})

		require.ErrorIs(t, err, user.ErrEmailTaken)
	})
}

func sampleSession() *app.Session {
	return &app.Session{
		AccessToken:  "access.jwt.value",
		RefreshToken: "refresh-opaque-value",
		ExpiresIn:    15 * time.Minute,
		User:         apptest.SeededUser(),
	}
}

func TestAuthService_Login(t *testing.T) {
	t.Parallel()

	t.Run("returns a session in the same shape as sessionResponse on the REST side", func(t *testing.T) {
		t.Parallel()

		uc := &apptest.FakeAuth{Session: sampleSession()}
		svc := &authService{auth: uc}

		resp, err := svc.Login(context.Background(), &userv1.LoginRequest{
			Email: "Natthawat@Example.com", Password: apptest.SeededPassword,
		})

		require.NoError(t, err)
		s := resp.GetSession()
		require.Equal(t, "access.jwt.value", s.GetAccessToken())
		require.Equal(t, "Bearer", s.GetTokenType())
		require.Equal(t, int64(900), s.GetExpiresIn())
		require.Equal(t, "refresh-opaque-value", s.GetRefreshToken())
		require.Equal(t, "natthawat@example.com", s.GetUser().GetEmail())
		// Forwarded to the very same use case REST calls, so lowercasing the email happens in its usual place.
		require.Equal(t, "Natthawat@Example.com", uc.LastLogin.Email)
	})

	t.Run("a failed login is passed on unchanged", func(t *testing.T) {
		t.Parallel()

		svc := &authService{auth: &apptest.FakeAuth{Err: user.ErrInvalidCredentials}}

		_, err := svc.Login(context.Background(), &userv1.LoginRequest{Email: "a@b.co", Password: "x"})

		require.ErrorIs(t, err, user.ErrInvalidCredentials)
	})
}

func TestAuthService_Refresh(t *testing.T) {
	t.Parallel()

	t.Run("rotating must return a whole new session", func(t *testing.T) {
		t.Parallel()

		uc := &apptest.FakeAuth{Session: sampleSession()}
		svc := &authService{auth: uc}

		resp, err := svc.Refresh(context.Background(), &userv1.RefreshRequest{RefreshToken: "refresh-old"})

		require.NoError(t, err)
		require.Equal(t, "access.jwt.value", resp.GetSession().GetAccessToken())
		require.Equal(t, "refresh-opaque-value", resp.GetSession().GetRefreshToken())
		require.Equal(t, "refresh-old", uc.LastRefresh)
	})

	t.Run("an unusable token is passed on unchanged", func(t *testing.T) {
		t.Parallel()

		svc := &authService{auth: &apptest.FakeAuth{Err: user.ErrUnauthorized}}

		_, err := svc.Refresh(context.Background(), &userv1.RefreshRequest{RefreshToken: "expired"})

		require.ErrorIs(t, err, user.ErrUnauthorized)
	})
}
