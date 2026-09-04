package user

import (
	"fmt"
	"net/mail"
	"strings"
)

// emailMaxLen is the longest address RFC 5321 allows on the wire.
const emailMaxLen = 254

// Email is a value object guaranteeing that the value inside has always been normalized.
// Lowercasing has to happen in exactly one place, otherwise A@x.com and a@x.com
// would both slip past the unique index.
type Email string

// NewEmail validates the format and returns the normalized value.
// It is the only way to build a valid Email from outside this package.
func NewEmail(raw string) (Email, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" {
		return "", ErrValidation{Field: "email", Reason: "must not be empty"}
	}
	if len(e) > emailMaxLen {
		return "", ErrValidation{Field: "email", Reason: fmt.Sprintf("longer than %d characters", emailMaxLen)}
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e {
		// Also rejects forms like `"Name" <a@x.com>`, because we want an addr-spec only.
		return "", ErrValidation{Field: "email", Reason: "invalid email format"}
	}
	if !strings.Contains(strings.SplitN(e, "@", 2)[1], ".") {
		return "", ErrValidation{Field: "email", Reason: "invalid email format"}
	}
	return Email(e), nil
}

func (e Email) String() string { return string(e) }
