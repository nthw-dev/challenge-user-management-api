// Package apptest holds the test doubles shared across packages — the driving-port fakes the inbound adapter tests use,
// plus the fixtures (clock, seeded user, password) that would otherwise be copied into every test package.
//
// REST and gRPC used to keep separate copies of the same fakes — so once the two transports' behavior drifted apart,
// no test could catch it, because each side compared against its own copy. Hence a single set here, shared by both.
// The doubles sit at the port level rather than the driver level, exactly like the fakes in the app package itself.
package apptest

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// ---- fixtures ----

const (
	SeededID       = "6702c1f4a3b19d0f9c4e2a71"
	SeededPassword = "Str0ng-Pass!"
)

var SeededAt = time.Date(2026, 9, 3, 9, 14, 22, 0, time.UTC)

// SeededUser is a single user that is "already in the database" — built via Hydrate, not New.
func SeededUser() *user.User {
	return user.Hydrate(SeededID, "Natthawat N.",
		user.Email("natthawat@example.com"), "$2a$12$hash", SeededAt, SeededAt)
}

func DiscardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Clock is a controllable app.Clock — tests move time forward instead of waiting for it.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

var _ app.Clock = (*Clock)(nil)

func NewClock() *Clock { return &Clock{now: SeededAt} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// ---- driving-port doubles ----

// FakeUsers answers with preconfigured values and records what the adapter passed in, for the test to inspect.
// It applies no rules of its own — a test that wants a validation failure sets Err.
type FakeUsers struct {
	User  *user.User
	Users []user.User
	Next  string
	Err   error

	LastID     string
	LastActor  string
	LastCreate app.CreateUserInput
	LastFilter app.ListFilter
	LastUpdate app.UpdateUserInput
}

var _ app.UserUseCase = (*FakeUsers)(nil)

func (f *FakeUsers) Create(_ context.Context, in app.CreateUserInput) (*user.User, error) {
	f.LastCreate = in
	return f.User, f.Err
}

func (f *FakeUsers) Get(_ context.Context, id string) (*user.User, error) {
	f.LastID = id
	return f.User, f.Err
}

// List resolves the filter through the real rule — so it echoes the limit the use case would have applied, without a copy of the rule.
func (f *FakeUsers) List(_ context.Context, filter app.ListFilter) (app.Page, error) {
	f.LastFilter = filter
	if f.Err != nil {
		return app.Page{}, f.Err
	}
	q, err := filter.Resolve()
	if err != nil {
		return app.Page{}, err
	}
	return app.Page{Users: f.Users, Limit: q.Limit, NextCursor: f.Next}, nil
}

func (f *FakeUsers) Update(_ context.Context, actorID, id string, in app.UpdateUserInput) (*user.User, error) {
	f.LastActor, f.LastID, f.LastUpdate = actorID, id, in
	return f.User, f.Err
}

func (f *FakeUsers) Delete(_ context.Context, actorID, id string) error {
	f.LastActor, f.LastID = actorID, id
	return f.Err
}

func (f *FakeUsers) Count(context.Context) (int64, error) { return int64(len(f.Users)), f.Err }

type FakeAuth struct {
	Session *app.Session
	Err     error

	LastLogin   app.LoginInput
	LastRefresh string
}

var _ app.AuthUseCase = (*FakeAuth)(nil)

func (f *FakeAuth) Login(_ context.Context, in app.LoginInput) (*app.Session, error) {
	f.LastLogin = in
	return f.Session, f.Err
}

func (f *FakeAuth) Refresh(_ context.Context, token string) (*app.Session, error) {
	f.LastRefresh = token
	return f.Session, f.Err
}

// Verifier accepts every token as belonging to SeededID, unless Err is set.
type Verifier struct{ Err error }

var _ app.TokenVerifier = Verifier{}

func (v Verifier) Verify(string) (string, error) {
	if v.Err != nil {
		return "", v.Err
	}
	return SeededID, nil
}
