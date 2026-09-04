// Package parity proves the two transports tell the same story: the same use case, the same fake, the same
// input — sent once over REST and once over gRPC — must come back with the same data, the same error code,
// the same field detail and a request id on both. Each transport's own tests check its shape; this one checks
// that the shapes agree, so the two cannot drift apart the way they once did.
//
// gRPC responses are compared through protojson with proto field names and unpopulated fields emitted, which lines them
// up with the REST JSON key for key; the only tolerated difference is that protojson prints 64-bit integers as strings.
package parity_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	grpcapi "github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpc"
	userv1 "github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpc/gen/user/v1"
	httpapi "github.com/nthw-dev/user-management-api/internal/adapter/inbound/http"
	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

const requestID = "parity-request-id"

// transports is both adapters wired to the very same fakes.
type transports struct {
	rest  http.Handler
	users userv1.UserServiceClient
	auth  userv1.AuthServiceClient
}

// newTransports takes the ports rather than the fakes themselves, so the same wiring serves both this file's
// preconfigured doubles and validation_test.go's real use cases.
func newTransports(t *testing.T, users app.UserUseCase, auth app.AuthUseCase, tokens app.TokenVerifier) transports {
	t.Helper()

	rest := httpapi.NewRouter(httpapi.Deps{Users: users, Auth: auth, Tokens: tokens, Logger: apptest.DiscardLogger()})

	srv := grpcapi.NewServer(grpcapi.Deps{
		Users: users, Auth: auth, Tokens: tokens, Logger: apptest.DiscardLogger(), RPCTimeout: 5 * time.Second,
	})
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return transports{rest: rest, users: userv1.NewUserServiceClient(conn), auth: userv1.NewAuthServiceClient(conn)}
}

// ---- REST side ----

type restResult struct {
	status  int
	body    map[string]any
	headers http.Header
}

func (tr transports) call(t *testing.T, method, path, body string, authed bool) restResult {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", requestID)
	if authed {
		req.Header.Set("Authorization", "Bearer any-token")
	}
	rec := httptest.NewRecorder()
	tr.rest.ServeHTTP(rec, req)

	out := restResult{status: rec.Code, headers: rec.Header()}
	if rec.Body.Len() > 0 {
		out.body = decodeJSON(t, rec.Body.Bytes())
	}
	return out
}

// ---- gRPC side ----

// grpcCtx carries the same token and request id the REST call carries.
func grpcCtx(authed bool) context.Context {
	md := metadata.Pairs("x-request-id", requestID)
	if authed {
		md.Append("authorization", "Bearer any-token")
	}
	return metadata.NewOutgoingContext(context.Background(), md)
}

// asJSON renders a response the way REST would — proto field names, no envelope — for a key-by-key comparison.
func asJSON(t *testing.T, m proto.Message) map[string]any {
	t.Helper()
	raw, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(m)
	require.NoError(t, err)
	return decodeJSON(t, raw)
}

// errorBody renders a gRPC status the way the REST error envelope looks, from the details both transports agree to carry.
func errorBody(t *testing.T, err error) map[string]any {
	t.Helper()
	st := status.Convert(err)
	body := map[string]any{"message": st.Message()}
	for _, d := range st.Details() {
		switch d := d.(type) {
		case *errdetails.ErrorInfo:
			body["code"] = d.GetReason()
			if rid, ok := d.GetMetadata()["request_id"]; ok {
				body["request_id"] = rid
			}
		case *errdetails.BadRequest:
			details := make([]any, 0, len(d.GetFieldViolations()))
			for _, v := range d.GetFieldViolations() {
				details = append(details, map[string]any{"field": v.GetField(), "issue": v.GetDescription()})
			}
			body["details"] = details
		}
	}
	return body
}

// ---- comparison ----

func decodeJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var out map[string]any
	require.NoError(t, dec.Decode(&out))
	return out
}

// sameData asserts the two bodies agree key for key. REST is the reference: a null there must be null or absent on gRPC,
// and a REST number may arrive from protojson as a decimal string (64-bit integers).
func sameData(t *testing.T, rest, grpc any, path string) {
	t.Helper()
	switch r := rest.(type) {
	case map[string]any:
		g, ok := grpc.(map[string]any)
		require.True(t, ok, "%s: gRPC has %T where REST has an object", path, grpc)
		for key, rv := range r {
			if rv == nil {
				require.Nil(t, g[key], "%s.%s: REST has null, gRPC has a value", path, key)
				continue
			}
			gv, present := g[key]
			require.True(t, present, "%s.%s: present on REST, missing on gRPC", path, key)
			sameData(t, rv, gv, path+"."+key)
		}
		for key, gv := range g {
			if _, present := r[key]; !present {
				require.Nil(t, gv, "%s.%s: present on gRPC, missing on REST", path, key)
			}
		}
	case []any:
		g, ok := grpc.([]any)
		require.True(t, ok, "%s: gRPC has %T where REST has an array", path, grpc)
		require.Len(t, g, len(r), "%s: different lengths", path)
		for i := range r {
			sameData(t, r[i], g[i], path+"["+strconv.Itoa(i)+"]")
		}
	case json.Number:
		if s, isString := grpc.(string); isString {
			require.Equal(t, r.String(), s, "%s", path)
			return
		}
		require.Equal(t, r, grpc, "%s", path)
	default:
		require.Equal(t, rest, grpc, "%s", path)
	}
}

// sameError asserts both transports report the same code, message, field detail and a request id.
func sameError(t *testing.T, rest restResult, err error) {
	t.Helper()
	require.NotNil(t, rest.body["error"], "REST answered no error body")
	restErr := rest.body["error"].(map[string]any)
	restErr["request_id"] = rest.body["request_id"]
	require.Equal(t, requestID, restErr["request_id"], "REST must echo the request id it was given")

	grpcErr := errorBody(t, err)
	require.Equal(t, requestID, grpcErr["request_id"], "gRPC must echo the request id it was given")
	sameData(t, restErr, grpcErr, "error")
}

// ---- the cases ----

func TestParity_User(t *testing.T) {
	t.Parallel()
	tr := newTransports(t, &apptest.FakeUsers{User: apptest.SeededUser()}, &apptest.FakeAuth{}, apptest.Verifier{})

	t.Run("get", func(t *testing.T) {
		rest := tr.call(t, http.MethodGet, "/api/v1/users/"+apptest.SeededID, "", true)
		resp, err := tr.users.GetUser(grpcCtx(true), &userv1.GetUserRequest{Id: apptest.SeededID})
		require.NoError(t, err)

		require.Equal(t, http.StatusOK, rest.status)
		sameData(t, rest.body["data"], asJSON(t, resp)["user"], "data")
	})

	t.Run("create", func(t *testing.T) {
		body := `{"name":"Natthawat N.","email":"natthawat@example.com","password":"` + apptest.SeededPassword + `"}`
		rest := tr.call(t, http.MethodPost, "/api/v1/users", body, true)
		resp, err := tr.users.CreateUser(grpcCtx(true), &userv1.CreateUserRequest{
			Name: "Natthawat N.", Email: "natthawat@example.com", Password: apptest.SeededPassword,
		})
		require.NoError(t, err)

		require.Equal(t, http.StatusCreated, rest.status)
		sameData(t, rest.body["data"], asJSON(t, resp)["user"], "data")
	})

	t.Run("update", func(t *testing.T) {
		rest := tr.call(t, http.MethodPatch, "/api/v1/users/"+apptest.SeededID, `{"name":"New Name"}`, true)
		resp, err := tr.users.UpdateUser(grpcCtx(true), &userv1.UpdateUserRequest{Id: apptest.SeededID, Name: proto.String("New Name")})
		require.NoError(t, err)

		require.Equal(t, http.StatusOK, rest.status)
		sameData(t, rest.body["data"], asJSON(t, resp)["user"], "data")
	})

	t.Run("delete answers with no body on either side", func(t *testing.T) {
		rest := tr.call(t, http.MethodDelete, "/api/v1/users/"+apptest.SeededID, "", true)
		resp, err := tr.users.DeleteUser(grpcCtx(true), &userv1.DeleteUserRequest{Id: apptest.SeededID})
		require.NoError(t, err)

		require.Equal(t, http.StatusNoContent, rest.status)
		require.Nil(t, rest.body)
		require.Empty(t, asJSON(t, resp))
	})
}

func TestParity_List(t *testing.T) {
	t.Parallel()

	t.Run("a page with more to come", func(t *testing.T) {
		t.Parallel()
		tr := newTransports(t, &apptest.FakeUsers{Users: []user.User{*apptest.SeededUser()}, Next: "6702c1f4a3b19d0f9c4e2a6f"},
			&apptest.FakeAuth{}, apptest.Verifier{})

		rest := tr.call(t, http.MethodGet, "/api/v1/users?limit=2&cursor=c1&query=nat", "", true)
		resp, err := tr.users.ListUsers(grpcCtx(true), &userv1.ListUsersRequest{Limit: proto.Int32(2), Cursor: "c1", Query: "nat"})
		require.NoError(t, err)

		g := asJSON(t, resp)
		sameData(t, rest.body["data"], g["users"], "data")
		sameData(t, rest.body["meta"], g["meta"], "meta")
	})

	t.Run("the last page, with no limit sent", func(t *testing.T) {
		t.Parallel()
		tr := newTransports(t, &apptest.FakeUsers{Users: []user.User{}}, &apptest.FakeAuth{}, apptest.Verifier{})

		rest := tr.call(t, http.MethodGet, "/api/v1/users", "", true)
		resp, err := tr.users.ListUsers(grpcCtx(true), &userv1.ListUsersRequest{})
		require.NoError(t, err)

		g := asJSON(t, resp)
		require.Equal(t, []any{}, rest.body["data"], "an empty page is [] on REST")
		require.Empty(t, resp.GetUsers(), "and empty on gRPC")
		sameData(t, rest.body["meta"], g["meta"], "meta")
	})
}

func TestParity_Session(t *testing.T) {
	t.Parallel()
	session := &app.Session{
		AccessToken: "access.jwt", RefreshToken: "refresh-opaque", ExpiresIn: 15 * time.Minute, User: apptest.SeededUser(),
	}
	tr := newTransports(t, &apptest.FakeUsers{}, &apptest.FakeAuth{Session: session}, apptest.Verifier{})

	t.Run("login", func(t *testing.T) {
		rest := tr.call(t, http.MethodPost, "/api/v1/auth/login", `{"email":"a@x.com","password":"pw"}`, false)
		resp, err := tr.auth.Login(grpcCtx(false), &userv1.LoginRequest{Email: "a@x.com", Password: "pw"})
		require.NoError(t, err)

		require.Equal(t, http.StatusOK, rest.status)
		sameData(t, rest.body["data"], asJSON(t, resp)["session"], "data")
	})

	t.Run("refresh", func(t *testing.T) {
		rest := tr.call(t, http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"old"}`, false)
		resp, err := tr.auth.Refresh(grpcCtx(false), &userv1.RefreshRequest{RefreshToken: "old"})
		require.NoError(t, err)

		sameData(t, rest.body["data"], asJSON(t, resp)["session"], "data")
	})
}

func TestParity_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		usersErr   error
		authErr    error
		verifier   apptest.Verifier
		restStatus int
		viaUpdate  bool // send the case through PATCH / UpdateUser, the verbs that can be forbidden
	}{
		{name: "user not found", usersErr: user.ErrNotFound, restStatus: http.StatusNotFound},
		{name: "email taken", usersErr: user.ErrEmailTaken, restStatus: http.StatusConflict},
		{name: "validation", usersErr: user.ErrValidation{Field: "email", Reason: "invalid email format"}, restStatus: http.StatusUnprocessableEntity},
		{
			name: "validation on several fields at once",
			usersErr: user.ValidationErrors{
				{Field: "name", Reason: "must be 1–100 characters"},
				{Field: "email", Reason: "invalid email format"},
			},
			restStatus: http.StatusUnprocessableEntity,
		},
		{name: "someone else's row", usersErr: user.ErrForbidden, restStatus: http.StatusForbidden, viaUpdate: true},
		{name: "invalid credentials", authErr: user.ErrInvalidCredentials, restStatus: http.StatusUnauthorized},
		{name: "refresh token cannot be honored", authErr: user.ErrUnauthorized, restStatus: http.StatusUnauthorized},
		{name: "no usable token", verifier: apptest.Verifier{Err: user.ErrUnauthorized}, restStatus: http.StatusUnauthorized},
		{name: "internal", usersErr: errors.New("mongo: connection refused"), restStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr := newTransports(t, &apptest.FakeUsers{Err: tt.usersErr}, &apptest.FakeAuth{Err: tt.authErr}, tt.verifier)

			var rest restResult
			var err error
			switch {
			case tt.authErr != nil:
				rest = tr.call(t, http.MethodPost, "/api/v1/auth/login", `{"email":"a@x.com","password":"pw"}`, false)
				_, err = tr.auth.Login(grpcCtx(false), &userv1.LoginRequest{Email: "a@x.com", Password: "pw"})
			case tt.viaUpdate:
				rest = tr.call(t, http.MethodPatch, "/api/v1/users/someone-else", `{"name":"n"}`, true)
				_, err = tr.users.UpdateUser(grpcCtx(true), &userv1.UpdateUserRequest{Id: "someone-else", Name: proto.String("n")})
			default:
				body := `{"name":"n","email":"e@x.com","password":"` + apptest.SeededPassword + `"}`
				rest = tr.call(t, http.MethodPost, "/api/v1/users", body, true)
				_, err = tr.users.CreateUser(grpcCtx(true), &userv1.CreateUserRequest{Name: "n", Email: "e@x.com", Password: apptest.SeededPassword})
			}

			require.Equal(t, tt.restStatus, rest.status)
			require.Error(t, err)
			sameError(t, rest, err)
		})
	}
}

// The request id a caller sends comes back on both transports — X-Request-ID on REST, x-request-id metadata on gRPC.
func TestParity_RequestIDIsEchoed(t *testing.T) {
	t.Parallel()
	tr := newTransports(t, &apptest.FakeUsers{User: apptest.SeededUser()}, &apptest.FakeAuth{}, apptest.Verifier{})

	rest := tr.call(t, http.MethodGet, "/api/v1/users/"+apptest.SeededID, "", true)
	require.Equal(t, requestID, rest.headers.Get("X-Request-ID"))

	var header metadata.MD
	_, err := tr.users.GetUser(grpcCtx(true), &userv1.GetUserRequest{Id: apptest.SeededID}, grpc.Header(&header))
	require.NoError(t, err)
	require.Equal(t, []string{requestID}, header.Get("x-request-id"))
}
