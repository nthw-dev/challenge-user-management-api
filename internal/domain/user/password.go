package user

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const passwordMinRunes = 8

// commonPasswords is a short list of values common enough that they should never pass.
// A real system would compare against a breach corpus (Have I Been Pwned, for example) instead.
var commonPasswords = map[string]struct{}{
	"password": {}, "password1": {}, "password123": {}, "12345678": {},
	"123456789": {}, "qwerty123": {}, "iloveyou": {}, "admin123": {},
	"letmein1": {}, "welcome1": {}, "changeme": {}, "p@ssw0rd": {},
}

// ValidatePassword is a domain rule just like name and email — so every transport gets the same rule for free.
// The maximum-length limit does not live here, because it is a constraint of the hash algorithm, not of the domain.
func ValidatePassword(plain string) error {
	if utf8.RuneCountInString(plain) < passwordMinRunes {
		return ErrValidation{Field: "password", Reason: fmt.Sprintf("must be at least %d characters", passwordMinRunes)}
	}
	if _, bad := commonPasswords[strings.ToLower(plain)]; bad {
		return ErrValidation{Field: "password", Reason: "too easy to guess"}
	}
	return nil
}
