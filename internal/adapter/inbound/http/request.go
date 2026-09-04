package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/respond"
)

// decode reads JSON into dst — that is all; the value read is passed on for the use case to judge.
func decode(r *http.Request, dst any) error {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return respond.ErrPayloadTooBig
		}
		// The decoder's message may reveal internal structure, so it is not passed on.
		return respond.ErrMalformedJSON
	}
	return nil
}
