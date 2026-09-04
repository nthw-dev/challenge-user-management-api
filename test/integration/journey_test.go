//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	httpapi "github.com/nthw-dev/user-management-api/internal/adapter/inbound/http"
	"github.com/nthw-dev/user-management-api/internal/adapter/outbound/clock"
	mongostore "github.com/nthw-dev/user-management-api/internal/adapter/outbound/mongo"
	"github.com/nthw-dev/user-management-api/internal/adapter/outbound/security"
	"github.com/nthw-dev/user-management-api/internal/adapter/outbound/token"
	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
)

// system is the whole application short of a network socket: the real router with its full middleware chain, the real
// use cases, real bcrypt, real JWT, and the real repositories on a MongoDB database private to this suite.
// It is what main.go wires, minus the listeners — so what passes here passes in the container too.
type system struct {
	api    http.Handler
	db     *mongo.Database
	tokens *token.JWTService
}

func bootSystem() system {
	ctx := context.Background()

	client, err := mongostore.Connect(ctx, mongostore.Options{
		URI: mongoURI, ConnTimeout: time.Minute, MaxConnIdleTime: 30 * time.Minute, MaxPoolSize: 10, MinPoolSize: 1,
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = client.Disconnect(context.Background()) })

	var suffix [6]byte
	_, err = rand.Read(suffix[:])
	Expect(err).NotTo(HaveOccurred())
	db := client.Database("it_journey_" + hex.EncodeToString(suffix[:]))
	DeferCleanup(func() { _ = db.Drop(context.Background()) })

	users := mongostore.NewUserRepo(db.Collection("users"))
	refresh := mongostore.NewRefreshTokenRepo(db.Collection("refresh_tokens"))
	Expect(users.EnsureIndexes(ctx)).To(Succeed())
	Expect(refresh.EnsureIndexes(ctx)).To(Succeed())

	sysClock := clock.System{}
	hasher := security.NewBcryptHasher(4) // bcrypt's minimum cost: the algorithm is real, only the work factor is turned down
	tokens := token.NewJWTService([]byte("integration-test-secret-that-is-long-enough"), "user-service", "user-service-api", sysClock)
	userSvc := app.NewUserService(users, refresh, hasher, sysClock)
	authSvc, err := app.NewAuthService(users, refresh, hasher, tokens, sysClock,
		app.AuthConfig{AccessTTL: 15 * time.Minute, RefreshTTL: 168 * time.Hour})
	Expect(err).NotTo(HaveOccurred())

	api := httpapi.NewRouter(httpapi.Deps{
		Users: userSvc, Auth: authSvc, Tokens: tokens,
		Logger: apptest.DiscardLogger(),
		Ready:  mongostore.Ready(client),
	})
	return system{api: api, db: db, tokens: tokens}
}

// reply is one HTTP exchange, with the JSON body already decoded when there was one.
type reply struct {
	*httptest.ResponseRecorder
	body map[string]any
}

func (r reply) data() map[string]any {
	Expect(r.body).To(HaveKey("data"))
	return r.body["data"].(map[string]any)
}

func (r reply) items() []any {
	Expect(r.body).To(HaveKey("data"))
	return r.body["data"].([]any)
}

func (r reply) meta() map[string]any {
	Expect(r.body).To(HaveKey("meta"))
	return r.body["meta"].(map[string]any)
}

func (r reply) errorBody() map[string]any {
	Expect(r.body).To(HaveKey("error"))
	return r.body["error"].(map[string]any)
}

func (sys system) call(method, path, body, bearer string) reply {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	sys.api.ServeHTTP(rec, req)

	out := reply{ResponseRecorder: rec}
	if rec.Body.Len() > 0 {
		Expect(json.Unmarshal(rec.Body.Bytes(), &out.body)).To(Succeed(), "body: %s", rec.Body.String())
	}
	return out
}

// refreshRows counts this user's rows in the refresh_tokens collection, optionally only the live or only the revoked ones.
func (sys system) refreshRows(userID string, filter bson.M) int64 {
	filter["user_id"] = userID
	n, err := sys.db.Collection("refresh_tokens").CountDocuments(context.Background(), filter)
	Expect(err).NotTo(HaveOccurred())
	return n
}

// userRows counts the documents in the users collection matching filter — the ground truth behind a 201 or a 409.
func (sys system) userRows(filter bson.M) int64 {
	n, err := sys.db.Collection("users").CountDocuments(context.Background(), filter)
	Expect(err).NotTo(HaveOccurred())
	return n
}

// register signs a user up and returns the id, for the specs that need a second account beside the one under test.
func (sys system) register(name, email, password string) string {
	res := sys.call(http.MethodPost, "/api/v1/auth/register",
		`{"name":"`+name+`","email":"`+email+`","password":"`+password+`"}`, "")
	Expect(res.ResponseRecorder).To(HaveHTTPStatus(http.StatusCreated))
	id, _ := res.data()["id"].(string)
	return id
}

// login answers the session for a user that exists.
func (sys system) login(email, password string) map[string]any {
	res := sys.call(http.MethodPost, "/api/v1/auth/login", `{"email":"`+email+`","password":"`+password+`"}`, "")
	Expect(res.ResponseRecorder).To(HaveHTTPStatus(http.StatusOK))
	return res.data()
}

var _ = Describe("one user's journey over REST, end to end on real MongoDB", Ordered, func() {
	const (
		email    = "journey@example.com"
		password = "Str0ng-Pass!"
	)
	var (
		sys      system
		userID   string
		access   string
		refresh1 string // from the login
		refresh2 string // from the first rotation
	)

	BeforeAll(func() { sys = bootSystem() })

	It("reports ready, because the readiness probe really pings the database", func() {
		res := sys.call(http.MethodGet, "/readyz", "", "")

		Expect(res.ResponseRecorder).To(HaveHTTPStatus(http.StatusOK))
		Expect(res.body).To(HaveKeyWithValue("status", "ok"))
	})

	It("signs up, and gets back an id, a Location header, and no trace of the password", func() {
		res := sys.call(http.MethodPost, "/api/v1/auth/register",
			`{"name":"Journey","email":"Journey@Example.com","password":"`+password+`"}`, "")

		Expect(res.ResponseRecorder).To(HaveHTTPStatus(http.StatusCreated))
		userID, _ = res.data()["id"].(string)
		Expect(userID).To(HaveLen(24), "an ObjectId, as 24 hex characters")
		Expect(res.ResponseRecorder).To(HaveHTTPHeaderWithValue("Location", "/api/v1/users/"+userID))
		Expect(res.data()).To(HaveKeyWithValue("email", email), "normalized to lower case")
		Expect(res.Body.String()).NotTo(ContainSubstring("password"))
		Expect(res.Header().Get("X-Request-ID")).NotTo(BeEmpty())
	})

	It("refuses the same email again in any case — the unique index speaks through the whole stack", func() {
		res := sys.call(http.MethodPost, "/api/v1/auth/register",
			`{"name":"Copycat","email":"JOURNEY@example.com","password":"`+password+`"}`, "")

		Expect(res.ResponseRecorder).To(HaveHTTPStatus(http.StatusConflict))
		Expect(res.errorBody()).To(HaveKeyWithValue("code", "EMAIL_TAKEN"))
	})

	It("keeps the protected routes closed without a token", func() {
		res := sys.call(http.MethodGet, "/api/v1/users/"+userID, "", "")

		Expect(res.ResponseRecorder).To(HaveHTTPStatus(http.StatusUnauthorized))
		Expect(res.ResponseRecorder).To(HaveHTTPHeaderWithValue("WWW-Authenticate", `Bearer realm="user-service"`))
		Expect(res.errorBody()).To(HaveKeyWithValue("code", "UNAUTHORIZED"))
	})

	It("logs in with the right password and gets a session whose access token verifies to this user", func() {
		res := sys.call(http.MethodPost, "/api/v1/auth/login", `{"email":"`+email+`","password":"`+password+`"}`, "")

		Expect(res.ResponseRecorder).To(HaveHTTPStatus(http.StatusOK))
		session := res.data()
		Expect(session).To(HaveKeyWithValue("token_type", "Bearer"))
		Expect(session).To(HaveKeyWithValue("expires_in", BeEquivalentTo(900)))
		Expect(session["user"]).To(HaveKeyWithValue("id", userID))
		access, _ = session["access_token"].(string)
		refresh1, _ = session["refresh_token"].(string)
		Expect(access).NotTo(BeEmpty())
		Expect(refresh1).To(HaveLen(64))

		subject, err := sys.tokens.Verify(access)
		Expect(err).NotTo(HaveOccurred())
		Expect(subject).To(Equal(userID))
	})

	It("answers a wrong password and an unknown email with one and the same 401", func() {
		wrongPassword := sys.call(http.MethodPost, "/api/v1/auth/login", `{"email":"`+email+`","password":"nope-nope"}`, "")
		unknownEmail := sys.call(http.MethodPost, "/api/v1/auth/login", `{"email":"nobody@example.com","password":"`+password+`"}`, "")

		Expect(wrongPassword.ResponseRecorder).To(HaveHTTPStatus(http.StatusUnauthorized))
		Expect(unknownEmail.ResponseRecorder).To(HaveHTTPStatus(http.StatusUnauthorized))
		Expect(wrongPassword.errorBody()).To(HaveKeyWithValue("code", "INVALID_CREDENTIALS"))
		Expect(unknownEmail.errorBody()).To(Equal(wrongPassword.errorBody()), "nothing may tell the two apart")
	})

	It("stores the refresh token hashed — the collection never holds the raw value", func() {
		Expect(sys.refreshRows(userID, bson.M{"token_hash": refresh1})).To(BeZero())
		Expect(sys.refreshRows(userID, bson.M{})).To(BeEquivalentTo(1))
	})

	It("reads, lists and updates the user with the access token", func() {
		got := sys.call(http.MethodGet, "/api/v1/users/"+userID, "", access)
		Expect(got.ResponseRecorder).To(HaveHTTPStatus(http.StatusOK))
		Expect(got.data()).To(HaveKeyWithValue("email", email))

		list := sys.call(http.MethodGet, "/api/v1/users?limit=1&query=JOURN", "", access)
		Expect(list.ResponseRecorder).To(HaveHTTPStatus(http.StatusOK))
		Expect(list.items()).To(HaveLen(1), "search ignores case, and the limit is honored")
		Expect(list.meta()).To(HaveKeyWithValue("limit", BeEquivalentTo(1)))
		Expect(list.meta()).To(HaveKeyWithValue("has_more", false))

		renamed := sys.call(http.MethodPatch, "/api/v1/users/"+userID, `{"name":"Journey Renamed"}`, access)
		Expect(renamed.ResponseRecorder).To(HaveHTTPStatus(http.StatusOK))
		Expect(renamed.data()).To(HaveKeyWithValue("name", "Journey Renamed"))
		Expect(renamed.data()).To(HaveKeyWithValue("email", email), "a field that was not sent is left alone")
	})

	It("names every field that failed at once, so a form can be fixed in a single round trip", func() {
		res := sys.call(http.MethodPost, "/api/v1/auth/register", `{"name":"  ","email":"not-an-email","password":"short"}`, "")

		Expect(res.ResponseRecorder).To(HaveHTTPStatus(http.StatusUnprocessableEntity))
		Expect(res.errorBody()).To(HaveKeyWithValue("code", "VALIDATION_ERROR"))
		details, _ := res.errorBody()["details"].([]any)
		Expect(details).To(HaveLen(3))
		Expect(details[0]).To(HaveKeyWithValue("field", "name"))
		Expect(details[1]).To(HaveKeyWithValue("field", "email"))
		Expect(details[2]).To(HaveKeyWithValue("field", "password"))
	})

	It("refuses to edit or delete anyone else's account — 403 FORBIDDEN, whether or not the row exists", func() {
		otherID := sys.register("Bystander", "bystander@example.com", password)

		edited := sys.call(http.MethodPatch, "/api/v1/users/"+otherID, `{"name":"Hijacked"}`, access)
		Expect(edited.ResponseRecorder).To(HaveHTTPStatus(http.StatusForbidden))
		Expect(edited.errorBody()).To(HaveKeyWithValue("code", "FORBIDDEN"))
		Expect(edited.Header().Get("WWW-Authenticate")).To(BeEmpty(), "the caller is authenticated; this is not a challenge")

		deleted := sys.call(http.MethodDelete, "/api/v1/users/"+otherID, "", access)
		Expect(deleted.ResponseRecorder).To(HaveHTTPStatus(http.StatusForbidden))

		ghost := sys.call(http.MethodDelete, "/api/v1/users/000000000000000000000099", "", access)
		Expect(ghost.ResponseRecorder).To(HaveHTTPStatus(http.StatusForbidden), "a row that does not exist answers the same — 403 must not double as an existence check")

		untouched := sys.call(http.MethodGet, "/api/v1/users/"+otherID, "", access)
		Expect(untouched.data()).To(HaveKeyWithValue("name", "Bystander"), "reads stay open; the bystander is unchanged")
	})

	It("rejects a limit out of range with the field named in the details", func() {
		res := sys.call(http.MethodGet, "/api/v1/users?limit=0", "", access)

		Expect(res.ResponseRecorder).To(HaveHTTPStatus(http.StatusUnprocessableEntity))
		Expect(res.errorBody()).To(HaveKeyWithValue("code", "VALIDATION_ERROR"))
		Expect(res.errorBody()["details"]).To(ContainElement(HaveKeyWithValue("field", "limit")))
	})

	It("rotates the refresh token: a new pair comes back, and the old row is marked revoked", func() {
		res := sys.call(http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"`+refresh1+`"}`, "")

		Expect(res.ResponseRecorder).To(HaveHTTPStatus(http.StatusOK))
		refresh2, _ = res.data()["refresh_token"].(string)
		Expect(refresh2).To(HaveLen(64))
		Expect(refresh2).NotTo(Equal(refresh1))
		newAccess, _ := res.data()["access_token"].(string)
		subject, err := sys.tokens.Verify(newAccess)
		Expect(err).NotTo(HaveOccurred())
		Expect(subject).To(Equal(userID))

		Expect(sys.refreshRows(userID, bson.M{})).To(BeEquivalentTo(2))
		Expect(sys.refreshRows(userID, bson.M{"revoked_at": bson.M{"$exists": true}})).To(BeEquivalentTo(1))
	})

	It("treats reuse of the rotated token as a leak and wipes every session, the fresh one included", func() {
		reuse := sys.call(http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"`+refresh1+`"}`, "")
		Expect(reuse.ResponseRecorder).To(HaveHTTPStatus(http.StatusUnauthorized))
		Expect(reuse.errorBody()).To(HaveKeyWithValue("code", "UNAUTHORIZED"))

		fresh := sys.call(http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"`+refresh2+`"}`, "")
		Expect(fresh.ResponseRecorder).To(HaveHTTPStatus(http.StatusUnauthorized))

		Expect(sys.refreshRows(userID, bson.M{"revoked_at": bson.M{"$exists": false}})).To(BeZero(), "no live session may remain")
	})

	It("still lets the user log in again afterwards — the account is not locked, only the sessions were cut", func() {
		res := sys.call(http.MethodPost, "/api/v1/auth/login", `{"email":"`+email+`","password":"`+password+`"}`, "")

		Expect(res.ResponseRecorder).To(HaveHTTPStatus(http.StatusOK))
		access, _ = res.data()["access_token"].(string)
		refresh1, _ = res.data()["refresh_token"].(string)
		Expect(sys.refreshRows(userID, bson.M{"revoked_at": bson.M{"$exists": false}})).To(BeEquivalentTo(1))
	})

	It("deletes the user, revoking its sessions with it, after which reads answer 404 and the refresh token 401", func() {
		Expect(sys.refreshRows(userID, bson.M{"revoked_at": bson.M{"$exists": false}})).To(BeEquivalentTo(1), "one live session going in")

		deleted := sys.call(http.MethodDelete, "/api/v1/users/"+userID, "", access)
		Expect(deleted.ResponseRecorder).To(HaveHTTPStatus(http.StatusNoContent))
		Expect(deleted.Body.Len()).To(BeZero())
		Expect(sys.refreshRows(userID, bson.M{"revoked_at": bson.M{"$exists": false}})).To(BeZero(), "the delete revoked every session; nothing waits for the TTL")

		again := sys.call(http.MethodDelete, "/api/v1/users/"+userID, "", access)
		Expect(again.ResponseRecorder).To(HaveHTTPStatus(http.StatusNotFound), "the truth is that it is gone")

		got := sys.call(http.MethodGet, "/api/v1/users/"+userID, "", access)
		Expect(got.ResponseRecorder).To(HaveHTTPStatus(http.StatusNotFound))
		Expect(got.errorBody()).To(HaveKeyWithValue("code", "USER_NOT_FOUND"))

		refreshed := sys.call(http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"`+refresh1+`"}`, "")
		Expect(refreshed.ResponseRecorder).To(HaveHTTPStatus(http.StatusUnauthorized), "a token whose user is gone is no token")
	})
})
