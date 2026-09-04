package app

import (
	"strconv"
	"time"

	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

type RefreshToken struct {
	ID        string
	UserID    string
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

func (rt RefreshToken) Revoked() bool { return rt.RevokedAt != nil }

// Session is the result of a single login.
type Session struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
	User         *user.User
}

// ---- use case input ----

type CreateUserInput struct {
	Name     string
	Email    string
	Password string
}

type LoginInput struct {
	Email    string
	Password string
}

// UpdateUserInput uses pointers to tell "this field was not sent" apart from "sent as an empty value".
type UpdateUserInput struct {
	Name  *string
	Email *string
}

func (in UpdateUserInput) Empty() bool { return in.Name == nil && in.Email == nil }

// UpdatePatch is what the repository receives — already past the domain invariants.
type UpdatePatch struct {
	Name      *string
	Email     *user.Email
	UpdatedAt time.Time
}

// ---- list reads ----

const (
	DefaultListLimit = 20
	MaxListLimit     = 100
)

// ListFilter is what a caller asks for. Limit is a pointer for the same reason UpdateUserInput's fields are:
// nil means "not sent", which gets the default, while a value that was sent — zero included — has to be within range.
type ListFilter struct {
	Limit  *int
	Cursor string
	Query  string
}

// ListQuery is what the repository receives — the filter with every rule already applied and the default filled in.
type ListQuery struct {
	Limit  int
	Cursor string
	Query  string
}

// Resolve applies the one paging rule there is, in the one place there is, so REST and gRPC reject with the same reason
// and the same message: a limit that was sent must be 1..MaxListLimit; a limit that was not sent becomes DefaultListLimit.
func (f ListFilter) Resolve() (ListQuery, error) {
	q := ListQuery{Limit: DefaultListLimit, Cursor: f.Cursor, Query: f.Query}
	if f.Limit == nil {
		return q, nil
	}
	switch {
	case *f.Limit < 1:
		return ListQuery{}, user.ErrValidation{Field: "limit", Reason: "must be a positive integer"}
	case *f.Limit > MaxListLimit:
		return ListQuery{}, user.ErrValidation{Field: "limit", Reason: "must not exceed " + strconv.Itoa(MaxListLimit)}
	}
	q.Limit = *f.Limit
	return q, nil
}

// Page is one page of a listing.
//
// Limit is the page size that was actually applied, so a transport can echo it back without re-deriving the default;
// NextCursor is opaque to everyone but the repository, and empty on the last page.
type Page struct {
	Users      []user.User
	Limit      int
	NextCursor string
}

func (p Page) HasMore() bool { return p.NextCursor != "" }
