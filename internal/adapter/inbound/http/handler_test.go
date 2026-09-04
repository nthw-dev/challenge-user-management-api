package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	// Register the OpenAPI spec the way main.go does, otherwise /swagger/doc.json has nothing real to serve.
	_ "github.com/nthw-dev/user-management-api/openapi"

	httpapi "github.com/nthw-dev/user-management-api/internal/adapter/inbound/http"
	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// newRouter fills in whatever a test did not care to set — the router itself refuses to build without every use case.
func newRouter(t *testing.T, d httpapi.Deps) http.Handler {
	t.Helper()
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if d.Users == nil {
		d.Users = &apptest.FakeUsers{}
	}
	if d.Auth == nil {
		d.Auth = &apptest.FakeAuth{}
	}
	if d.Tokens == nil {
		d.Tokens = apptest.Verifier{}
	}
	return httpapi.NewRouter(d)
}

// A router missing a use case would only fail on its first request; it has to fail at boot instead.
func TestNewRouter_RefusesIncompleteDeps(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t, "httpapi: Deps is missing Users, Auth, Tokens", func() { httpapi.NewRouter(httpapi.Deps{}) })
}

func do(t *testing.T, h http.Handler, method, path, body string, authed bool) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if authed {
		req.Header.Set("Authorization", "Bearer test-token")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Field string `json:"field"`
			Issue string `json:"issue"`
		} `json:"details"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorResponse {
	t.Helper()
	var body errorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

// ---- tests ----

func TestGetUser_NotFound(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{Err: user.ErrNotFound}})

	rec := do(t, h, http.MethodGet, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71", "", true)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "USER_NOT_FOUND", decodeError(t, rec).Error.Code)
	require.NotEmpty(t, rec.Header().Get("X-Request-ID"))
}

func TestGetUser_Success(t *testing.T) {
	t.Parallel()

	uc := &apptest.FakeUsers{User: apptest.SeededUser()}
	h := newRouter(t, httpapi.Deps{Users: uc})

	rec := do(t, h, http.MethodGet, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71", "", true)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "6702c1f4a3b19d0f9c4e2a71", uc.LastID)

	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "natthawat@example.com", body.Data["email"])
	require.Equal(t, "2026-09-03T09:14:22Z", body.Data["created_at"], "the time must be RFC 3339 in UTC")

	// The password must not leak out in any form whatsoever.
	require.NotContains(t, rec.Body.String(), "password")
	require.NotContains(t, rec.Body.String(), "$2a$12$hash")
}

// A malformed id is a 422 on the field, not a 404 — and the message never says "ObjectId".
func TestGetUser_MalformedID(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{Err: user.ErrValidation{Field: "id", Reason: "invalid format"}}})

	rec := do(t, h, http.MethodGet, "/api/v1/users/not-an-id", "", true)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := decodeError(t, rec)
	require.Equal(t, "VALIDATION_ERROR", body.Error.Code)
	require.Equal(t, "id", body.Error.Details[0].Field)
	require.NotContains(t, rec.Body.String(), "ObjectId")
}

func TestProtectedRoutes_RequireToken(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{
		Users:  &apptest.FakeUsers{User: apptest.SeededUser()},
		Tokens: apptest.Verifier{Err: user.ErrUnauthorized},
	})

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/users"},
		{http.MethodPost, "/api/v1/users"},
		{http.MethodGet, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71"},
		{http.MethodPatch, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71"},
		{http.MethodDelete, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			rec := do(t, h, tt.method, tt.path, "{}", false)

			require.Equal(t, http.StatusUnauthorized, rec.Code)
			require.Equal(t, "UNAUTHORIZED", decodeError(t, rec).Error.Code)
			require.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")
		})
	}
}

func TestListUsers_Pagination(t *testing.T) {
	t.Parallel()

	uc := &apptest.FakeUsers{Users: []user.User{*apptest.SeededUser()}, Next: "6702c1f4a3b19d0f9c4e2a6f"}
	h := newRouter(t, httpapi.Deps{Users: uc})

	rec := do(t, h, http.MethodGet, "/api/v1/users?limit=2", "", true)

	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			Limit      int     `json:"limit"`
			NextCursor *string `json:"next_cursor"`
			HasMore    bool    `json:"has_more"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	require.Equal(t, 2, body.Meta.Limit)
	require.True(t, body.Meta.HasMore)
	require.NotNil(t, body.Meta.NextCursor)
	require.Equal(t, "6702c1f4a3b19d0f9c4e2a6f", *body.Meta.NextCursor)
}

func TestListUsers_NoNextPage(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{Users: []user.User{}}})

	rec := do(t, h, http.MethodGet, "/api/v1/users", "", true)

	require.Equal(t, http.StatusOK, rec.Code)
	// The client should not have to guess — next_cursor is null and data is [], not null.
	require.Contains(t, rec.Body.String(), `"next_cursor":null`)
	require.Contains(t, rec.Body.String(), `"data":[]`)
}

// The range check is the use case's rule; what this transport has to get right is forwarding the value as sent and mapping the refusal.
func TestListUsers_LimitOverCap(t *testing.T) {
	t.Parallel()

	uc := &apptest.FakeUsers{Err: user.ErrValidation{Field: "limit", Reason: "must not exceed 100"}}
	h := newRouter(t, httpapi.Deps{Users: uc})

	rec := do(t, h, http.MethodGet, "/api/v1/users?limit=1000", "", true)

	require.Equal(t, 1000, *uc.LastFilter.Limit, "the value must reach the use case untouched")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := decodeError(t, rec)
	require.Equal(t, "VALIDATION_ERROR", body.Error.Code)
	require.Equal(t, "limit", body.Error.Details[0].Field)
}

// cursor and query are passed through untouched — the repository reads the cursor, the query is a plain substring.
func TestListUsers_ForwardsCursorAndQuery(t *testing.T) {
	t.Parallel()

	uc := &apptest.FakeUsers{Users: []user.User{}}
	h := newRouter(t, httpapi.Deps{Users: uc})

	rec := do(t, h, http.MethodGet, "/api/v1/users?cursor=6702c1f4a3b19d0f9c4e2a71&query=som", "", true)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "6702c1f4a3b19d0f9c4e2a71", uc.LastFilter.Cursor)
	require.Equal(t, "som", uc.LastFilter.Query)
	require.Nil(t, uc.LastFilter.Limit, "no limit sent means nil, so the use case applies the default")
}

// A cursor that is not an ObjectId is refused by the repository as a validation failure on the field — this side maps it to 422.
func TestListUsers_BadCursor(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{Err: user.ErrValidation{Field: "cursor", Reason: "invalid format"}}})

	rec := do(t, h, http.MethodGet, "/api/v1/users?cursor=nope", "", true)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, "cursor", decodeError(t, rec).Error.Details[0].Field)
}

// A number that cannot be parsed is a transport concern; the acceptable range is a rule of the use case.
func TestListUsers_LimitNotANumber(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{}})

	rec := do(t, h, http.MethodGet, "/api/v1/users?limit=abc", "", true)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, "limit", decodeError(t, rec).Error.Details[0].Field)
}

func TestUpdateUser_PartialPatch(t *testing.T) {
	t.Parallel()

	uc := &apptest.FakeUsers{User: apptest.SeededUser()}
	h := newRouter(t, httpapi.Deps{Users: uc})

	rec := do(t, h, http.MethodPatch, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71",
		`{"name":"Natthawat Narin"}`, true)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, uc.LastUpdate.Name)
	require.Equal(t, "Natthawat Narin", *uc.LastUpdate.Name)
	require.Nil(t, uc.LastUpdate.Email, "a field that was not sent must be nil, not the empty value")
	require.Equal(t, apptest.SeededID, uc.LastActor, "the verified subject must reach the use case as the actor")
}

// Every other refusal PATCH can meet, mapped: the shape is the same envelope, the code says which.
func TestUpdateUser_ErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		code   string
		fields []string
	}{
		{name: "no such user", err: user.ErrNotFound, status: http.StatusNotFound, code: "USER_NOT_FOUND"},
		{name: "email already taken", err: user.ErrEmailTaken, status: http.StatusConflict, code: "EMAIL_TAKEN"},
		{name: "empty body", err: user.ErrValidation{Field: "body", Reason: "at least one field is required"},
			status: http.StatusUnprocessableEntity, code: "VALIDATION_ERROR", fields: []string{"body"}},
		{name: "a bad name and a bad email, together",
			err:    user.ValidationErrors{{Field: "name", Reason: "empty"}, {Field: "email", Reason: "bad"}},
			status: http.StatusUnprocessableEntity, code: "VALIDATION_ERROR", fields: []string{"name", "email"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{Err: tt.err}})

			rec := do(t, h, http.MethodPatch, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71", `{"name":"x"}`, true)

			require.Equal(t, tt.status, rec.Code)
			body := decodeError(t, rec)
			require.Equal(t, tt.code, body.Error.Code)
			var got []string
			for _, d := range body.Error.Details {
				got = append(got, d.Field)
			}
			require.Equal(t, tt.fields, got)
		})
	}
}

func TestDeleteUser_NotFound(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{Err: user.ErrNotFound}})

	rec := do(t, h, http.MethodDelete, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71", "", true)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "USER_NOT_FOUND", decodeError(t, rec).Error.Code)
}

// The use case decides who may change what; the transport has to map its refusal to 403 — and not to 401,
// which would carry a WWW-Authenticate challenge and tell the client to try logging in again.
func TestUpdateAndDelete_Forbidden(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		method, body string
	}{
		{http.MethodPatch, `{"name":"hijacked"}`},
		{http.MethodDelete, ""},
	} {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			// One fake per subtest: the fake records what it was handed, and the subtests run in parallel.
			h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{Err: user.ErrForbidden}})

			rec := do(t, h, tt.method, "/api/v1/users/000000000000000000000099", tt.body, true)

			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Equal(t, "FORBIDDEN", decodeError(t, rec).Error.Code)
			require.Empty(t, rec.Header().Get("WWW-Authenticate"), "the caller is authenticated; a challenge would be wrong")
		})
	}
}

func TestDeleteUser_NoContent(t *testing.T) {
	t.Parallel()

	uc := &apptest.FakeUsers{}
	h := newRouter(t, httpapi.Deps{Users: uc})

	rec := do(t, h, http.MethodDelete, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71", "", true)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Empty(t, rec.Body.String())
	require.Equal(t, apptest.SeededID, uc.LastActor)
}

// POST /users is register with a token: the same body, the same use case, the same answers.
func TestCreateUser(t *testing.T) {
	t.Parallel()

	t.Run("created, with a Location header and no token in the answer", func(t *testing.T) {
		t.Parallel()
		uc := &apptest.FakeUsers{User: apptest.SeededUser()}
		h := newRouter(t, httpapi.Deps{Users: uc})

		rec := do(t, h, http.MethodPost, "/api/v1/users",
			`{"name":"Natthawat N.","email":"Natthawat@Example.com","password":"Str0ng-Passw0rd!"}`, true)

		require.Equal(t, http.StatusCreated, rec.Code)
		require.Equal(t, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71", rec.Header().Get("Location"))
		require.Equal(t, "Natthawat@Example.com", uc.LastCreate.Email, "forwarded as sent; the domain normalizes")
		require.NotContains(t, rec.Body.String(), "access_token")
	})

	t.Run("a duplicate email is 409", func(t *testing.T) {
		t.Parallel()
		h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{Err: user.ErrEmailTaken}})

		rec := do(t, h, http.MethodPost, "/api/v1/users",
			`{"name":"Somchai","email":"a@example.com","password":"Str0ng-Passw0rd!"}`, true)

		require.Equal(t, http.StatusConflict, rec.Code)
		require.Equal(t, "EMAIL_TAKEN", decodeError(t, rec).Error.Code)
	})
}

func TestRegister_Created(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{User: apptest.SeededUser()}})

	rec := do(t, h, http.MethodPost, "/api/v1/auth/register",
		`{"name":"Natthawat N.","email":"Natthawat@Example.com","password":"Str0ng-Passw0rd!"}`, false)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71", rec.Header().Get("Location"))
	// Signing up returns no token — issuing tokens happens in one place, /auth/login.
	require.NotContains(t, rec.Body.String(), "access_token")
}

func TestRegister_EmailTaken(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{Err: user.ErrEmailTaken}})

	rec := do(t, h, http.MethodPost, "/api/v1/auth/register",
		`{"name":"Somchai","email":"a@example.com","password":"Str0ng-Passw0rd!"}`, false)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "EMAIL_TAKEN", decodeError(t, rec).Error.Code)
}

// The rules live in the domain alone and the handler merely forwards — what needs proving is that an ErrValidation becomes a 422 carrying the field name.
func TestRegister_ValidationDetails(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{
		Err: user.ErrValidation{Field: "email", Reason: "invalid email format"},
	}})

	rec := do(t, h, http.MethodPost, "/api/v1/auth/register",
		`{"name":"Somchai","email":"not-an-email","password":"Str0ng-Passw0rd!"}`, false)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	body := decodeError(t, rec)
	require.Equal(t, "VALIDATION_ERROR", body.Error.Code)
	require.Len(t, body.Error.Details, 1)
	require.Equal(t, "email", body.Error.Details[0].Field)
	require.Equal(t, "invalid email format", body.Error.Details[0].Issue)
}

// When the domain rejects several fields, every one of them is in details, in the domain's order — one round trip to fix a form.
func TestRegister_ValidationDetails_EveryField(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{
		Err: user.ValidationErrors{
			{Field: "name", Reason: "must be 1–100 characters"},
			{Field: "email", Reason: "invalid email format"},
			{Field: "password", Reason: "must be at least 8 characters"},
		},
	}})

	rec := do(t, h, http.MethodPost, "/api/v1/auth/register", `{"name":"","email":"nope","password":"short"}`, false)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := decodeError(t, rec)
	require.Equal(t, "VALIDATION_ERROR", body.Error.Code)
	require.Len(t, body.Error.Details, 3)
	require.Equal(t, "name", body.Error.Details[0].Field)
	require.Equal(t, "email", body.Error.Details[1].Field)
	require.Equal(t, "password", body.Error.Details[2].Field)
}

func TestRegister_MalformedJSON(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{}})

	rec := do(t, h, http.MethodPost, "/api/v1/auth/register", `{"name":`, false)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "MALFORMED_JSON", decodeError(t, rec).Error.Code)
}

// A body over the cap is not malformed JSON — it is too large, and the status has to say so.
func TestRegister_PayloadTooLarge(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{}})
	body := `{"name":"` + strings.Repeat("x", 1<<20) + `"}`

	rec := do(t, h, http.MethodPost, "/api/v1/auth/register", body, false)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Equal(t, "PAYLOAD_TOO_LARGE", decodeError(t, rec).Error.Code)
}

func TestLogin_Success(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Auth: &apptest.FakeAuth{Session: &app.Session{
		AccessToken:  "eyJhbGciOiJIUzI1NiJ9.payload.sig",
		RefreshToken: "1f4ec9",
		ExpiresIn:    15 * time.Minute,
		User:         apptest.SeededUser(),
	}}})

	rec := do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"email":"natthawat@example.com","password":"Str0ng-Passw0rd!"}`, false)

	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data struct {
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
			ExpiresIn   int    `json:"expires_in"`
			User        struct {
				Email string `json:"email"`
			} `json:"user"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "Bearer", body.Data.TokenType)
	require.Equal(t, 900, body.Data.ExpiresIn)
	require.Equal(t, "natthawat@example.com", body.Data.User.Email)
}

func TestRefresh_Success(t *testing.T) {
	t.Parallel()

	auth := &apptest.FakeAuth{Session: &app.Session{
		AccessToken: "new.access", RefreshToken: "new-refresh", ExpiresIn: 15 * time.Minute, User: apptest.SeededUser(),
	}}
	h := newRouter(t, httpapi.Deps{Auth: auth})

	rec := do(t, h, http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"old-refresh"}`, false)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "old-refresh", auth.LastRefresh, "the token is forwarded as sent")
	var body struct {
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			TokenType    string `json:"token_type"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "new.access", body.Data.AccessToken)
	require.Equal(t, "new-refresh", body.Data.RefreshToken, "a whole new pair comes back")
	require.Equal(t, "Bearer", body.Data.TokenType)
}

// Unknown, expired, rotated or raced — the use case answers one error, and this side maps it to one 401 without saying which.
func TestRefresh_Unauthorized(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Auth: &apptest.FakeAuth{Err: user.ErrUnauthorized}})

	rec := do(t, h, http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"spent"}`, false)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "UNAUTHORIZED", decodeError(t, rec).Error.Code)
	require.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")
}

func TestRefresh_MalformedJSON(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{})

	rec := do(t, h, http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":`, false)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "MALFORMED_JSON", decodeError(t, rec).Error.Code)
}

func TestLogin_InvalidCredentials(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Auth: &apptest.FakeAuth{Err: user.ErrInvalidCredentials}})

	rec := do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"email":"nobody@example.com","password":"whatever-long"}`, false)

	require.Equal(t, http.StatusUnauthorized, rec.Code)

	body := decodeError(t, rec)
	require.Equal(t, "INVALID_CREDENTIALS", body.Error.Code)
	// The message must not say whether it was the email or the password that was wrong.
	require.NotContains(t, body.Error.Message, "not found")
	require.NotEmpty(t, body.RequestID)
}

func TestHealthAndReadiness(t *testing.T) {
	t.Parallel()

	t.Run("healthz touches no dependency", func(t *testing.T) {
		t.Parallel()
		h := newRouter(t, httpapi.Deps{})

		require.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/healthz", "", false).Code)
	})

	t.Run("readyz answers 503 while a dependency is not ready", func(t *testing.T) {
		t.Parallel()
		h := newRouter(t, httpapi.Deps{
			Ready: func(context.Context) error { return user.ErrNotFound },
		})

		rec := do(t, h, http.MethodGet, "/readyz", "", false)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Contains(t, rec.Body.String(), `"unavailable"`)
	})

	// Once shutdown has begun the answer is "no" regardless of how the database is doing — and without asking it.
	t.Run("readyz answers 503 draining once shutdown has begun, without pinging", func(t *testing.T) {
		t.Parallel()
		h := newRouter(t, httpapi.Deps{
			Ready:    func(context.Context) error { t.Fatal("the database must not be pinged while draining"); return nil },
			Draining: func() bool { return true },
		})

		rec := do(t, h, http.MethodGet, "/readyz", "", false)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Contains(t, rec.Body.String(), `"draining"`)
	})

	// Liveness must stay green while draining — flipping it too would have the orchestrator restart a pod that is merely finishing up.
	t.Run("healthz keeps answering 200 while draining", func(t *testing.T) {
		t.Parallel()
		h := newRouter(t, httpapi.Deps{Draining: func() bool { return true }})

		require.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/healthz", "", false).Code)
	})
}

func TestMetricsEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("served when a registry is wired, and it counts the requests it saw", func(t *testing.T) {
		t.Parallel()
		reg := prometheus.NewRegistry()
		h := newRouter(t, httpapi.Deps{Registry: reg, Users: &apptest.FakeUsers{User: apptest.SeededUser()}})
		do(t, h, http.MethodGet, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71", "", true)

		rec := do(t, h, http.MethodGet, "/metrics", "", false)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), `http_requests_total{method="GET",route="/api/v1/users/{id}",status="200"} 1`)
		require.Contains(t, rec.Body.String(), "http_request_duration_seconds")
	})

	t.Run("not mounted without a registry", func(t *testing.T) {
		t.Parallel()
		h := newRouter(t, httpapi.Deps{})

		require.Equal(t, http.StatusNotFound, do(t, h, http.MethodGet, "/metrics", "", false).Code)
	})
}

func TestSwaggerUI(t *testing.T) {
	t.Parallel()

	t.Run("with Docs off there must be no /swagger route at all", func(t *testing.T) {
		t.Parallel()
		h := newRouter(t, httpapi.Deps{})

		rec := do(t, h, http.MethodGet, "/swagger/index.html", "", false)
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Equal(t, "NOT_FOUND", decodeError(t, rec).Error.Code)
	})

	t.Run("with Docs on, /swagger redirects to the UI page", func(t *testing.T) {
		t.Parallel()
		h := newRouter(t, httpapi.Deps{Docs: true})

		rec := do(t, h, http.MethodGet, "/swagger", "", false)
		require.Equal(t, http.StatusFound, rec.Code)
		require.Equal(t, "/swagger/index.html", rec.Header().Get("Location"))
	})

	t.Run("with Docs on, the served spec must contain the API's real routes", func(t *testing.T) {
		t.Parallel()
		h := newRouter(t, httpapi.Deps{Docs: true})

		rec := do(t, h, http.MethodGet, "/swagger/doc.json", "", false)
		require.Equal(t, http.StatusOK, rec.Code)

		var spec struct {
			Swagger string                    `json:"swagger"`
			Paths   map[string]map[string]any `json:"paths"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &spec))
		require.Equal(t, "2.0", spec.Swagger)
		require.Contains(t, spec.Paths, "/api/v1/users/{id}")
		require.Contains(t, spec.Paths["/api/v1/users/{id}"], "patch")
	})
}

func TestGRPCConsole(t *testing.T) {
	t.Parallel()

	t.Run("without a GRPCConsole there must be no /grpcui route", func(t *testing.T) {
		t.Parallel()
		h := newRouter(t, httpapi.Deps{})

		rec := do(t, h, http.MethodGet, "/grpcui/", "", false)
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Equal(t, "NOT_FOUND", decodeError(t, rec).Error.Code)
	})

	t.Run("/grpcui redirects to the trailing-slash form, otherwise links in the page point at the wrong level", func(t *testing.T) {
		t.Parallel()
		h := newRouter(t, httpapi.Deps{GRPCConsole: http.NotFoundHandler()})

		rec := do(t, h, http.MethodGet, "/grpcui", "", false)
		require.Equal(t, http.StatusFound, rec.Code)
		require.Equal(t, "/grpcui/", rec.Header().Get("Location"))
	})

	t.Run("the prefix is stripped before handing over, so the inner handler sees the path it expects", func(t *testing.T) {
		t.Parallel()

		var seen []string
		spy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusOK)
		})
		h := newRouter(t, httpapi.Deps{GRPCConsole: spy})

		require.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/grpcui/", "", false).Code)
		// Calling an RPC is a POST rather than a GET — so this route has to accept every method, not just GET.
		require.Equal(t, http.StatusOK,
			do(t, h, http.MethodPost, "/grpcui/invoke/user.v1.UserService.GetUser", "{}", false).Code)

		require.Equal(t, []string{
			"GET /",
			"POST /invoke/user.v1.UserService.GetUser",
		}, seen)
	})
}

func TestUnknownRouteAndMethod(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &apptest.FakeUsers{}})

	notFound := do(t, h, http.MethodGet, "/api/v1/nope", "", true)
	require.Equal(t, http.StatusNotFound, notFound.Code)
	require.Equal(t, "NOT_FOUND", decodeError(t, notFound).Error.Code)

	badMethod := do(t, h, http.MethodPut, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71", "{}", true)
	require.Equal(t, http.StatusMethodNotAllowed, badMethod.Code)
	require.Equal(t, "METHOD_NOT_ALLOWED", decodeError(t, badMethod).Error.Code)
}

func TestPanicIsRecovered(t *testing.T) {
	t.Parallel()

	h := newRouter(t, httpapi.Deps{Users: &panickingUseCase{}})

	rec := do(t, h, http.MethodGet, "/api/v1/users/6702c1f4a3b19d0f9c4e2a71", "", true)

	require.Equal(t, http.StatusInternalServerError, rec.Code)

	body := decodeError(t, rec)
	require.Equal(t, "INTERNAL", body.Error.Code)
	// Internal details must not leak out with the response.
	require.NotContains(t, rec.Body.String(), "panic")
	require.NotEmpty(t, body.RequestID)
}

type panickingUseCase struct{ apptest.FakeUsers }

func (p *panickingUseCase) Get(context.Context, string) (*user.User, error) {
	panic("boom")
}
