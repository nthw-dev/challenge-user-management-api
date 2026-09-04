// The cases in parity_test.go inject a ready-made error into the driving-port fakes, which proves the two transports
// map one error identically — but not that a body with fields missing produces that error in the first place.
//
// This file closes that gap: the real UserService and AuthService sit behind both adapters, so a request travels the
// whole way down to the domain's invariants and back. It is the test for the "required fields and email format"
// requirement — one rule, written once in internal/domain/user, reaching a REST caller and a gRPC caller alike.
package parity_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	userv1 "github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpc/gen/user/v1"
	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// ---- driven-port stubs ----
//
// Every method fails loudly, because a valid request is not what is under test here: each case below must be refused
// before anything is stored or hashed, so a stub that is reached at all means the invariant was checked too late.

type refusingRepo struct{ t *testing.T }

var _ app.UserRepository = (*refusingRepo)(nil)

func (r *refusingRepo) Create(context.Context, *user.User) error {
	r.t.Fatal("the repository was written to for input that should never have got past validation")
	return nil
}

// FindByID answers with the seeded user so that a PATCH reaching it is a real read, not a fatal —
// the empty-patch case has to be refused before this point, and TestParity_ValidationHappensBeforeStorage proves it is.
func (r *refusingRepo) FindByID(context.Context, string) (*user.User, error) {
	return apptest.SeededUser(), nil
}

func (r *refusingRepo) FindByEmail(context.Context, user.Email) (*user.User, error) {
	return nil, user.ErrNotFound
}

func (r *refusingRepo) List(context.Context, app.ListQuery) (app.Page, error) {
	r.t.Fatal("List is not part of these cases")
	return app.Page{}, nil
}

func (r *refusingRepo) Update(context.Context, string, app.UpdatePatch) (*user.User, error) {
	r.t.Fatal("the repository was written to for input that should never have got past validation")
	return nil, nil
}

func (r *refusingRepo) Delete(context.Context, string) error {
	r.t.Fatal("Delete is not part of these cases")
	return nil
}

func (r *refusingRepo) Count(context.Context) (int64, error) { return 0, nil }

type refusingRefreshRepo struct{ t *testing.T }

var _ app.RefreshTokenRepository = (*refusingRefreshRepo)(nil)

func (r *refusingRefreshRepo) Store(context.Context, app.RefreshToken) error {
	r.t.Fatal("a session was issued for input that should never have got past validation")
	return nil
}

func (r *refusingRefreshRepo) FindByHash(context.Context, string) (*app.RefreshToken, error) {
	return nil, app.ErrRefreshTokenNotFound
}

func (r *refusingRefreshRepo) Revoke(context.Context, string, time.Time) error           { return nil }
func (r *refusingRefreshRepo) RevokeAllForUser(context.Context, string, time.Time) error { return nil }

// countingHasher is deliberately not bcrypt: these tests care only about how often it is asked to hash,
// since user.New promises to check every field before paying for a hash.
type countingHasher struct{ hashes int }

var _ app.PasswordHasher = (*countingHasher)(nil)

func (h *countingHasher) Hash(plain string) (string, error) {
	h.hashes++
	return "hashed:" + plain, nil
}

func (h *countingHasher) Compare(hash, plain string) error {
	if hash == "hashed:"+plain {
		return nil
	}
	return app.ErrPasswordMismatch
}

type stubIssuer struct{}

func (stubIssuer) Issue(userID string, _ time.Duration) (string, error) {
	return "access-" + userID, nil
}

// realTransports wires both adapters onto the genuine use cases, in place of the driving-port fakes.
// The verifier answers with SeededID, so the authenticated cases act as the seeded user on their own row.
func newRealTransports(t *testing.T) (transports, *countingHasher) {
	t.Helper()

	hasher := &countingHasher{}
	repo := &refusingRepo{t: t}
	refresh := &refusingRefreshRepo{t: t}
	clock := apptest.NewClock()

	users := app.NewUserService(repo, refresh, hasher, clock)
	auth, err := app.NewAuthService(repo, refresh, hasher, stubIssuer{}, clock,
		app.AuthConfig{AccessTTL: time.Minute, RefreshTTL: time.Hour})
	require.NoError(t, err)

	// Constructing the decoy hash is the one hash the suite expects before any request is made.
	hasher.hashes = 0

	return newTransports(t, users, auth, apptest.Verifier{}), hasher
}

// fieldIssues pulls the per-field detail out of a REST error body, in the order it was sent.
func fieldIssues(t *testing.T, rest restResult) map[string]string {
	t.Helper()

	body, ok := rest.body["error"].(map[string]any)
	require.True(t, ok, "no error body")
	raw, ok := body["details"].([]any)
	require.True(t, ok, "the error carries no field detail: %v", body)

	out := make(map[string]string, len(raw))
	for _, each := range raw {
		d := each.(map[string]any)
		out[d["field"].(string)] = d["issue"].(string)
	}
	return out
}

// TestParity_MissingRequiredFields is the requirement itself: a body with nothing in it must come back naming every
// field that is required, on both transports, in one round trip rather than one field per attempt.
func TestParity_MissingRequiredFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		body   string
		req    *userv1.CreateUserRequest
		fields map[string]string
	}{
		{
			name: "nothing at all",
			body: `{}`,
			req:  &userv1.CreateUserRequest{},
			fields: map[string]string{
				"name":     "must be 1–100 characters",
				"email":    "must not be empty",
				"password": "must be at least 8 characters",
			},
		},
		{
			name: "only the name was sent",
			body: `{"name":"Natthawat N."}`,
			req:  &userv1.CreateUserRequest{Name: "Natthawat N."},
			fields: map[string]string{
				"email":    "must not be empty",
				"password": "must be at least 8 characters",
			},
		},
		{
			// Whitespace is not a value: the domain trims before it measures, so this is as empty as the case above.
			name: "sent, but blank",
			body: `{"name":"   ","email":"  ","password":"        "}`,
			req:  &userv1.CreateUserRequest{Name: "   ", Email: "  ", Password: "        "},
			fields: map[string]string{
				"name":  "must be 1–100 characters",
				"email": "must not be empty",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr, hasher := newRealTransports(t)

			// POST /users and POST /auth/register are the same use case; only the access control differs,
			// so the rules must land identically on both.
			for _, route := range []struct {
				path   string
				authed bool
			}{
				{path: "/api/v1/users", authed: true},
				{path: "/api/v1/auth/register", authed: false},
			} {
				rest := tr.call(t, http.MethodPost, route.path, tt.body, route.authed)
				require.Equal(t, http.StatusUnprocessableEntity, rest.status, route.path)
				require.Equal(t, "VALIDATION_ERROR", rest.body["error"].(map[string]any)["code"], route.path)
				require.Equal(t, tt.fields, fieldIssues(t, rest), route.path)
			}

			_, err := tr.users.CreateUser(grpcCtx(true), tt.req)
			require.Equal(t, codes.InvalidArgument, status.Code(err))

			// The same case once more through REST, to compare the two answers field for field.
			rest := tr.call(t, http.MethodPost, "/api/v1/users", tt.body, true)
			sameError(t, rest, err)

			require.Zero(t, hasher.hashes,
				"the password was hashed for input that was going to be rejected anyway")
		})
	}
}

// Register is the public door — the same rules have to hold there, on both transports.
func TestParity_MissingRequiredFieldsOnRegister(t *testing.T) {
	t.Parallel()
	tr, _ := newRealTransports(t)

	rest := tr.call(t, http.MethodPost, "/api/v1/auth/register", `{}`, false)
	require.Equal(t, http.StatusUnprocessableEntity, rest.status)

	_, err := tr.auth.Register(grpcCtx(false), &userv1.RegisterRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	sameError(t, rest, err)
}

// TestParity_EmailFormat is the second half of the requirement. The cases are about the format alone,
// so name and password are always valid and only the email decides the answer.
func TestParity_EmailFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		email string
		issue string
	}{
		{name: "no at sign", email: "natthawat.example.com", issue: "invalid email format"},
		{name: "no domain", email: "natthawat@", issue: "invalid email format"},
		{name: "no local part", email: "@example.com", issue: "invalid email format"},
		{name: "the domain has no dot", email: "natthawat@localhost", issue: "invalid email format"},
		{name: "a display name is not an address", email: `"Nat" <nat@example.com>`, issue: "invalid email format"},
		{name: "spaces inside", email: "nat thawat@example.com", issue: "invalid email format"},
		{name: "longer than RFC 5321 allows", email: longEmail(), issue: "longer than 254 characters"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr, hasher := newRealTransports(t)

			body := `{"name":"Natthawat N.","email":` + quote(tt.email) + `,"password":"` + apptest.SeededPassword + `"}`
			rest := tr.call(t, http.MethodPost, "/api/v1/users", body, true)
			require.Equal(t, http.StatusUnprocessableEntity, rest.status)
			require.Equal(t, map[string]string{"email": tt.issue}, fieldIssues(t, rest))

			_, err := tr.users.CreateUser(grpcCtx(true), &userv1.CreateUserRequest{
				Name: "Natthawat N.", Email: tt.email, Password: apptest.SeededPassword,
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			sameError(t, rest, err)

			require.Zero(t, hasher.hashes, "the password was hashed despite the email being invalid")
		})
	}
}

// A well-formed address must pass — otherwise the cases above could be satisfied by a rule that rejects everything.
// It travels only as far as the repository, which is where this suite stops it.
func TestParity_AcceptedEmailReachesTheRepository(t *testing.T) {
	t.Parallel()

	for _, email := range []string{
		"natthawat@example.com",
		"nat.thawat+tag@sub.example.co.th",
		"NATTHAWAT@EXAMPLE.COM", // normalized on the way in, not refused
	} {
		t.Run(email, func(t *testing.T) {
			t.Parallel()

			hasher := &countingHasher{}
			stored := make(chan *user.User, 1)
			repo := &capturingRepo{refusingRepo: refusingRepo{t: t}, stored: stored}
			users := app.NewUserService(repo, &refusingRefreshRepo{t: t}, hasher, apptest.NewClock())
			tr := newTransports(t, users, &apptest.FakeAuth{}, apptest.Verifier{})

			body := `{"name":"Natthawat N.","email":` + quote(email) + `,"password":"` + apptest.SeededPassword + `"}`
			rest := tr.call(t, http.MethodPost, "/api/v1/users", body, true)
			require.Equal(t, http.StatusCreated, rest.status, "a valid address was refused: %v", rest.body)

			u := <-stored
			require.Equal(t, strings.ToLower(email), u.Email.String(),
				"the address that reached storage was not normalized")
		})
	}
}

// capturingRepo accepts the write and hands the entity back to the test, so a valid request can be followed
// all the way to the point where it would have been persisted.
type capturingRepo struct {
	refusingRepo
	stored chan *user.User
}

func (r *capturingRepo) Create(_ context.Context, u *user.User) error {
	r.stored <- u
	return nil
}

// PATCH with an empty body is the one required-field rule that lives in the use case rather than the domain,
// because "at least one of" is not an invariant of a user — it is what a patch means.
func TestParity_EmptyPatchIsRefused(t *testing.T) {
	t.Parallel()
	tr, _ := newRealTransports(t)

	rest := tr.call(t, http.MethodPatch, "/api/v1/users/"+apptest.SeededID, `{}`, true)
	require.Equal(t, http.StatusUnprocessableEntity, rest.status)
	require.Equal(t,
		map[string]string{"body": "at least one field is required (name or email)"},
		fieldIssues(t, rest))

	_, err := tr.users.UpdateUser(grpcCtx(true), &userv1.UpdateUserRequest{Id: apptest.SeededID})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	sameError(t, rest, err)
}

// A field sent as an empty value is a different thing from a field not sent, and PATCH has to tell them apart:
// this one is refused on the field, not ignored.
func TestParity_PatchWithBlankFieldIsRefused(t *testing.T) {
	t.Parallel()
	tr, _ := newRealTransports(t)

	rest := tr.call(t, http.MethodPatch, "/api/v1/users/"+apptest.SeededID, `{"name":"","email":"nope"}`, true)
	require.Equal(t, http.StatusUnprocessableEntity, rest.status)
	require.Equal(t, map[string]string{
		"name":  "must be 1–100 characters",
		"email": "invalid email format",
	}, fieldIssues(t, rest))

	_, err := tr.users.UpdateUser(grpcCtx(true), &userv1.UpdateUserRequest{
		Id: apptest.SeededID, Name: proto.String(""), Email: proto.String("nope"),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	sameError(t, rest, err)
}

// The password rule is a domain rule like the other two, so it must arrive the same way on both transports.
func TestParity_PasswordRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		password string
		issue    string
	}{
		{name: "too short", password: "Str0ng!", issue: "must be at least 8 characters"},
		{name: "too easy to guess", password: "password123", issue: "too easy to guess"},
		{name: "guessable whatever the case", password: "PassWord123", issue: "too easy to guess"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr, hasher := newRealTransports(t)

			body := `{"name":"Natthawat N.","email":"natthawat@example.com","password":` + quote(tt.password) + `}`
			rest := tr.call(t, http.MethodPost, "/api/v1/users", body, true)
			require.Equal(t, http.StatusUnprocessableEntity, rest.status)
			require.Equal(t, map[string]string{"password": tt.issue}, fieldIssues(t, rest))

			_, err := tr.users.CreateUser(grpcCtx(true), &userv1.CreateUserRequest{
				Name: "Natthawat N.", Email: "natthawat@example.com", Password: tt.password,
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			sameError(t, rest, err)

			require.Zero(t, hasher.hashes, "a rejected password was hashed anyway")
		})
	}
}

// Login and Refresh deliberately do NOT report a missing field: saying "email is required" would confirm to an
// attacker which part of a guess was wrong. Both answer exactly as a wrong password does, on both transports.
// This is the test that would fail if someone "fixed" the two by adding required-field validation to them.
func TestParity_CredentialsNeverReportMissingFields(t *testing.T) {
	t.Parallel()

	t.Run("login", func(t *testing.T) {
		t.Parallel()
		tr, _ := newRealTransports(t)

		rest := tr.call(t, http.MethodPost, "/api/v1/auth/login", `{}`, false)
		require.Equal(t, http.StatusUnauthorized, rest.status)
		body := rest.body["error"].(map[string]any)
		require.Equal(t, "INVALID_CREDENTIALS", body["code"])
		require.Nil(t, body["details"], "the answer to a login must not say which field was missing")

		_, err := tr.auth.Login(grpcCtx(false), &userv1.LoginRequest{})
		require.Equal(t, codes.Unauthenticated, status.Code(err))
		sameError(t, rest, err)
	})

	t.Run("refresh", func(t *testing.T) {
		t.Parallel()
		tr, _ := newRealTransports(t)

		rest := tr.call(t, http.MethodPost, "/api/v1/auth/refresh", `{}`, false)
		require.Equal(t, http.StatusUnauthorized, rest.status)
		body := rest.body["error"].(map[string]any)
		require.Equal(t, "UNAUTHORIZED", body["code"])
		require.Nil(t, body["details"], "the answer to a refresh must not say which field was missing")

		_, err := tr.auth.Refresh(grpcCtx(false), &userv1.RefreshRequest{})
		require.Equal(t, codes.Unauthenticated, status.Code(err))
		sameError(t, rest, err)
	})
}

// A body that is not JSON at all cannot reach the use case, so it is the one failure REST answers on its own —
// gRPC has no counterpart, since the wire format is the contract there.
func TestValidation_MalformedBodyIsNotAValidationError(t *testing.T) {
	t.Parallel()
	tr, _ := newRealTransports(t)

	for _, body := range []string{``, `{"name":`, `not json`} {
		rest := tr.call(t, http.MethodPost, "/api/v1/auth/register", body, false)
		require.Equal(t, http.StatusBadRequest, rest.status, "body %q", body)
		require.Equal(t, "MALFORMED_JSON", rest.body["error"].(map[string]any)["code"], "body %q", body)
	}
}

// ---- helpers ----

// longEmail is one character past the RFC 5321 limit, and valid in every other respect.
func longEmail() string {
	const domain = "@example.com"
	local := make([]byte, 255-len(domain))
	for i := range local {
		local[i] = 'a'
	}
	return string(local) + domain
}

// quote renders a value as a JSON string, so an address containing quotes can be sent as written.
func quote(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
