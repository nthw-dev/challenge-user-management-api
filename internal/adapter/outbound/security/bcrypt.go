// Package security is the outbound adapter for password hashing.
package security

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// BcryptHasher implement app.PasswordHasher
//
// A cost of 12 makes a single hash take hundreds of milliseconds.
// That slowness is the feature, not a drawback — it is what makes offline password guessing expensive.
type BcryptHasher struct{ cost int }

var _ app.PasswordHasher = BcryptHasher{}

func NewBcryptHasher(cost int) BcryptHasher {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		cost = bcrypt.DefaultCost
	}
	return BcryptHasher{cost: cost}
}

// Hash translates bcrypt's 72-byte limit into an ErrValidation right here,
// because it is a constraint of this particular algorithm rather than a domain rule — swap the hasher and the rule goes with it.
func (h BcryptHasher) Hash(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), h.cost)
	if errors.Is(err, bcrypt.ErrPasswordTooLong) {
		return "", user.ErrValidation{Field: "password", Reason: "longer than 72 bytes"}
	}
	if err != nil {
		return "", fmt.Errorf("bcrypt hash: %w", err)
	}
	return string(b), nil
}

// Compare answers a wrong password with the port's own sentinel, so the use case never has to know bcrypt's.
// Any other failure — a stored hash that is not bcrypt at all, for instance — is passed on as what it is: an infrastructure error.
func (h BcryptHasher) Compare(hash, plain string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return app.ErrPasswordMismatch
		}
		return fmt.Errorf("bcrypt compare: %w", err)
	}
	return nil
}
