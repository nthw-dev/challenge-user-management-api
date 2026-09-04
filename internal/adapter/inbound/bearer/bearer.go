// Package bearer pulls the token out of a "Bearer <token>" value, so REST and gRPC share one implementation.
//
// It used to be written in two places and the two drifted apart — one side ignored the case of the word Bearer, the other did not,
// so the same token passed through one transport but not the other.
package bearer

import "strings"

// Scheme is the RFC 6750 authorization scheme — the one word every transport prints when it hands a token out or asks for one.
const Scheme = "Bearer"

const prefix = Scheme + " "

// Token returns the token and true when the value is in "Bearer <token>" form, per RFC 6750.
// The scheme name is case-insensitive per RFC 7235, and an empty token counts as absent.
func Token(value string) (string, bool) {
	if len(value) <= len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(value[len(prefix):])
	return tok, tok != ""
}
