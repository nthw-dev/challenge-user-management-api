package app

import (
	"context"
	"errors"

	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// UserService is the user-management use case.
// It knows nothing of HTTP and nothing of MongoDB — only the ports declared in ports.go.
type UserService struct {
	repo    UserRepository
	refresh RefreshTokenRepository
	hasher  PasswordHasher
	clock   Clock
}

var _ UserUseCase = (*UserService)(nil)

func NewUserService(repo UserRepository, refresh RefreshTokenRepository, hasher PasswordHasher, clock Clock) *UserService {
	return &UserService{repo: repo, refresh: refresh, hasher: hasher, clock: clock}
}

func (s *UserService) Create(ctx context.Context, in CreateUserInput) (*user.User, error) {
	// The domain constructor enforces the name, email and password invariants, and only then calls the hasher.
	u, err := user.New(in.Name, in.Email, in.Password, s.hasher.Hash, s.clock.Now())
	if err != nil {
		return nil, err
	}

	// We deliberately do not pre-check for a duplicate email with a query, because two concurrent requests would both pass.
	// The unique index is the arbiter, and the adapter translates E11000 back into ErrEmailTaken.
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *UserService) Get(ctx context.Context, id string) (*user.User, error) {
	if err := requireID(id); err != nil {
		return nil, err
	}
	return s.repo.FindByID(ctx, id)
}

// List is where the paging rules are applied — once, for every transport — by resolving the filter before the repository sees it.
func (s *UserService) List(ctx context.Context, f ListFilter) (Page, error) {
	q, err := f.Resolve()
	if err != nil {
		return Page{}, err
	}

	page, err := s.repo.List(ctx, q)
	if err != nil {
		return Page{}, err
	}
	page.Limit = q.Limit
	return page, nil
}

func (s *UserService) Update(ctx context.Context, actorID, id string, in UpdateUserInput) (*user.User, error) {
	if err := requireID(id); err != nil {
		return nil, err
	}
	if err := requireSelf(actorID, id); err != nil {
		return nil, err
	}
	if in.Empty() {
		return nil, user.ErrValidation{Field: "body", Reason: "at least one field is required (name or email)"}
	}

	// Read the current state first, so that mutations go through the domain's own methods.
	// The invariants are therefore enforced in their usual place rather than restated in this layer.
	current, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	now := s.clock.Now()
	patch := UpdatePatch{UpdatedAt: now.UTC()}

	// Both fields are checked before either is written, and every failure is reported — the same courtesy Create extends.
	var invalid user.ValidationErrors
	if in.Name != nil {
		if err := current.Rename(*in.Name, now); err != nil {
			invalid = append(invalid, asValidation(err))
		} else {
			name := current.Name
			patch.Name = &name
		}
	}
	if in.Email != nil {
		if err := current.ChangeEmail(*in.Email, now); err != nil {
			invalid = append(invalid, asValidation(err))
		} else {
			email := current.Email
			patch.Email = &email
		}
	}
	if len(invalid) > 0 {
		return nil, invalid
	}

	return s.repo.Update(ctx, id, patch)
}

// Delete removes the user and then every refresh token they still hold — a deleted account must not be able to
// come back through /auth/refresh, and its rows should not linger until the TTL index gets to them.
// The order matters: the row goes first, so a failed revocation leaves nothing that a retry would find (it answers 404).
func (s *UserService) Delete(ctx context.Context, actorID, id string) error {
	if err := requireID(id); err != nil {
		return err
	}
	if err := requireSelf(actorID, id); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	return s.refresh.RevokeAllForUser(ctx, id, s.clock.Now())
}

func (s *UserService) Count(ctx context.Context) (int64, error) {
	return s.repo.Count(ctx)
}

// requireID rejects an empty id before it reaches the repository, so every verb answers the same way.
// The id's format is the repository's business; only its presence is checked here.
func requireID(id string) error {
	if id == "" {
		return user.ErrValidation{Field: "id", Reason: "must not be empty"}
	}
	return nil
}

// requireSelf is the whole of the authorization policy: a caller may change their own row and nobody else's.
// It runs before the repository is read, so a foreign id is refused the same way whether or not it exists —
// the answer must not double as an existence check. A role that may act on others would be added here.
func requireSelf(actorID, id string) error {
	if actorID == "" || actorID != id {
		return user.ErrForbidden
	}
	return nil
}

// asValidation narrows an error the domain's mutators return. They only ever return ErrValidation; anything else
// would be a programming error, and it is not hidden — it panics, as user.New does for the same case.
func asValidation(err error) user.ErrValidation {
	var v user.ErrValidation
	if !errors.As(err, &v) {
		panic("app: domain mutator returned a non-validation error: " + err.Error())
	}
	return v
}
