// Package user is the core of the system — it imports nothing outside the standard library.
// A test enforces that rule, at internal/domain/user/deps_test.go.
package user

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	nameMinRunes = 1
	nameMaxRunes = 100
)

type User struct {
	ID           string
	Name         string
	Email        Email
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// New is the single place that enforces every invariant of a new user.
//
// Every field is checked before anything is hashed — hashing is expensive by design, so we should not pay for input
// that would be rejected anyway — and every failure is reported together as a ValidationErrors, so a caller fixing a
// form learns about all of them at once. hash is injected as a function, so the domain does not know it is bcrypt
// and needs no extra import.
func New(name, email, password string, hash func(string) (string, error), now time.Time) (*User, error) {
	var invalid ValidationErrors

	n, err := normalizeName(name)
	invalid = collect(invalid, err)
	e, err := NewEmail(email)
	invalid = collect(invalid, err)
	invalid = collect(invalid, ValidatePassword(password))
	if len(invalid) > 0 {
		return nil, invalid
	}

	h, err := hash(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	return &User{
		Name:         n,
		Email:        e,
		PasswordHash: h,
		CreatedAt:    now.UTC(),
		UpdatedAt:    now.UTC(),
	}, nil
}

// Hydrate reassembles an entity from data that has already passed the invariants.
//
// For storage adapters and test doubles only — it bypasses every rule New enforces, because what was persisted is the truth.
// An inbound adapter must never call it; user input goes through New.
func Hydrate(id, name string, email Email, passwordHash string, createdAt, updatedAt time.Time) *User {
	return &User{
		ID:           id,
		Name:         name,
		Email:        email,
		PasswordHash: passwordHash,
		CreatedAt:    createdAt.UTC(),
		UpdatedAt:    updatedAt.UTC(),
	}
}

// Rename and ChangeEmail re-check the invariants before mutating.
// The fields stay exported so adapters can read them and call Hydrate — callers in the application layer must use these two methods only.
func (u *User) Rename(name string, now time.Time) error {
	n, err := normalizeName(name)
	if err != nil {
		return err
	}
	u.Name = n
	u.UpdatedAt = now.UTC()
	return nil
}

func (u *User) ChangeEmail(email string, now time.Time) error {
	e, err := NewEmail(email)
	if err != nil {
		return err
	}
	u.Email = e
	u.UpdatedAt = now.UTC()
	return nil
}

// collect appends err to the list when it is a validation failure. The invariants above only ever return
// ErrValidation, so anything else would be a programming error — and it is not silently dropped: it panics.
func collect(list ValidationErrors, err error) ValidationErrors {
	if err == nil {
		return list
	}
	var v ErrValidation
	if !errors.As(err, &v) {
		panic("user: invariant returned a non-validation error: " + err.Error())
	}
	return append(list, v)
}

func normalizeName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if c := utf8.RuneCountInString(n); c < nameMinRunes || c > nameMaxRunes {
		return "", ErrValidation{Field: "name", Reason: fmt.Sprintf("must be %d–%d characters", nameMinRunes, nameMaxRunes)}
	}
	return n, nil
}
