package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// Because the ports are declared in the application layer, building a double is just writing a struct
// with the full method set — no code generator needed, and finer control over the behavior.

// fakeHasher is fast enough to run in every test, unlike real bcrypt, which costs hundreds of milliseconds.
//
// It keeps every property the tests care about — the result contains no raw password,
// and a wrong password is reported with the same sentinel the real adapter uses.
type fakeHasher struct {
	hashErr    error
	compareErr error // simulates a comparison that fails for a reason other than a mismatch, e.g. a corrupt stored hash
}

func (h fakeHasher) Hash(plain string) (string, error) {
	if h.hashErr != nil {
		return "", h.hashErr
	}
	sum := sha256.Sum256([]byte("fake-hash:" + plain))
	return hex.EncodeToString(sum[:]), nil
}

func (h fakeHasher) Compare(hash, plain string) error {
	if h.compareErr != nil {
		return h.compareErr
	}
	want, err := h.Hash(plain)
	if err != nil {
		return err
	}
	if hash != want {
		return app.ErrPasswordMismatch
	}
	return nil
}

type fakeIssuer struct{ err error }

func (i fakeIssuer) Issue(userID string, ttl time.Duration) (string, error) {
	if i.err != nil {
		return "", i.err
	}
	return fmt.Sprintf("access-token:%s:%s", userID, ttl), nil
}

// fakeUserRepo mirrors the real repository's observable behavior: ids sort in insertion order,
// the cursor is the id of the last item on the previous page, and search ignores case.
type fakeUserRepo struct {
	mu      sync.Mutex
	seq     int
	byID    map[string]*user.User
	byEmail map[string]*user.User

	// A hook for forcing an error in the test cases that want one.
	createErr error
	calls     []string
}

func newFakeRepo() *fakeUserRepo {
	return &fakeUserRepo{byID: map[string]*user.User{}, byEmail: map[string]*user.User{}}
}

func (f *fakeUserRepo) nextID() string {
	f.seq++
	return fmt.Sprintf("%024x", f.seq)
}

func (f *fakeUserRepo) record(call string) { f.calls = append(f.calls, call) }

func (f *fakeUserRepo) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeUserRepo) Create(_ context.Context, u *user.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Create")

	if f.createErr != nil {
		return f.createErr
	}
	if _, dup := f.byEmail[u.Email.String()]; dup {
		return user.ErrEmailTaken // mimics the behavior of the unique index
	}

	u.ID = f.nextID()
	f.byID[u.ID], f.byEmail[u.Email.String()] = u, u
	return nil
}

func (f *fakeUserRepo) FindByID(_ context.Context, id string) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("FindByID")

	u, ok := f.byID[id]
	if !ok {
		return nil, user.ErrNotFound
	}
	clone := *u
	return &clone, nil
}

func (f *fakeUserRepo) FindByEmail(_ context.Context, email user.Email) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("FindByEmail")

	u, ok := f.byEmail[email.String()]
	if !ok {
		return nil, user.ErrNotFound
	}
	clone := *u
	return &clone, nil
}

func (f *fakeUserRepo) List(_ context.Context, q app.ListQuery) (app.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("List")

	query := strings.ToLower(q.Query)
	matches := make([]user.User, 0, len(f.byID))
	for _, u := range f.byID {
		if q.Cursor != "" && u.ID >= q.Cursor {
			continue
		}
		if query != "" &&
			!strings.Contains(strings.ToLower(u.Name), query) &&
			!strings.Contains(u.Email.String(), query) {
			continue
		}
		matches = append(matches, *u)
	}
	// Newest first, like the real repository's sort on _id.
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID > matches[j].ID })

	page := app.Page{Users: matches}
	if len(matches) > q.Limit {
		page.Users = matches[:q.Limit]
		page.NextCursor = page.Users[len(page.Users)-1].ID
	}
	return page, nil
}

func (f *fakeUserRepo) Update(_ context.Context, id string, p app.UpdatePatch) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Update")

	u, ok := f.byID[id]
	if !ok {
		return nil, user.ErrNotFound
	}
	if p.Email != nil {
		if other, dup := f.byEmail[p.Email.String()]; dup && other.ID != id {
			return nil, user.ErrEmailTaken
		}
		delete(f.byEmail, u.Email.String())
		u.Email = *p.Email
		f.byEmail[u.Email.String()] = u
	}
	if p.Name != nil {
		u.Name = *p.Name
	}
	u.UpdatedAt = p.UpdatedAt

	clone := *u
	return &clone, nil
}

func (f *fakeUserRepo) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Delete")

	u, ok := f.byID[id]
	if !ok {
		return user.ErrNotFound
	}
	delete(f.byID, id)
	delete(f.byEmail, u.Email.String())
	return nil
}

func (f *fakeUserRepo) Count(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Count")
	return int64(len(f.byID)), nil
}

type fakeRefreshRepo struct {
	mu     sync.Mutex
	seq    int
	byHash map[string]*app.RefreshToken

	storeErr error
}

func newFakeRefreshRepo() *fakeRefreshRepo {
	return &fakeRefreshRepo{byHash: map[string]*app.RefreshToken{}}
}

func (f *fakeRefreshRepo) failStores(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.storeErr = err
}

func (f *fakeRefreshRepo) Store(_ context.Context, rt app.RefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.storeErr != nil {
		return f.storeErr
	}
	f.seq++
	rt.ID = fmt.Sprintf("rt-%d", f.seq)
	f.byHash[rt.TokenHash] = &rt
	return nil
}

func (f *fakeRefreshRepo) FindByHash(_ context.Context, hash string) (*app.RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rt, ok := f.byHash[hash]
	if !ok {
		return nil, app.ErrRefreshTokenNotFound
	}
	clone := *rt
	return &clone, nil
}

func (f *fakeRefreshRepo) Revoke(_ context.Context, id string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rt := range f.byHash {
		if rt.ID == id && rt.RevokedAt == nil {
			t := now
			rt.RevokedAt = &t
			return nil
		}
	}
	return app.ErrRefreshTokenNotFound
}

func (f *fakeRefreshRepo) RevokeAllForUser(_ context.Context, userID string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rt := range f.byHash {
		if rt.UserID == userID && rt.RevokedAt == nil {
			t := now
			rt.RevokedAt = &t
		}
	}
	return nil
}

// count is every token ever stored, revoked or not — the number that tells a rotation race apart from an orphan.
func (f *fakeRefreshRepo) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byHash)
}

func (f *fakeRefreshRepo) activeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, rt := range f.byHash {
		if rt.RevokedAt == nil {
			n++
		}
	}
	return n
}

// ---- wiring helpers for tests ----

type suite struct {
	users   *app.UserService
	auth    *app.AuthService
	repo    *fakeUserRepo
	refresh *fakeRefreshRepo
	clock   *apptest.Clock
}

func newSuite(t *testing.T) *suite {
	t.Helper()
	return newSuiteWith(t, fakeHasher{})
}

func newSuiteWith(t *testing.T, hasher fakeHasher) *suite {
	t.Helper()

	repo := newFakeRepo()
	refresh := newFakeRefreshRepo()
	clk := apptest.NewClock()
	users := app.NewUserService(repo, refresh, hasher, clk)
	auth, err := app.NewAuthService(repo, refresh, hasher, fakeIssuer{}, clk, app.AuthConfig{
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 168 * time.Hour,
	})
	require.NoError(t, err)

	return &suite{users: users, auth: auth, repo: repo, refresh: refresh, clock: clk}
}

// seed puts a user straight into the repository and returns it with its assigned id,
// so a test never has to list-and-search just to learn the id of what it created.
func (s *suite) seed(t *testing.T, email string) *user.User {
	t.Helper()

	u, err := user.New("Seeded", email, apptest.SeededPassword, fakeHasher{}.Hash, apptest.SeededAt)
	require.NoError(t, err)
	require.NoError(t, s.repo.Create(context.Background(), u))
	s.repo.calls = nil
	return u
}
