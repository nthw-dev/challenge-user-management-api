package app

import "errors"

// Errors that belong to the application layer rather than the domain — they describe the ports' contract,
// so an adapter implementing a port and the use case calling it agree on the same sentinel.
var (
	// ErrPasswordMismatch is what PasswordHasher.Compare returns for a wrong password.
	// Anything else coming back from Compare means the comparison itself failed, which is an infrastructure problem.
	ErrPasswordMismatch = errors.New("password does not match")

	// ErrRefreshTokenNotFound is what RefreshTokenRepository returns when no live or revoked token matches.
	ErrRefreshTokenNotFound = errors.New("refresh token not found")
)
