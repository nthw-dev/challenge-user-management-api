// Package middleware collects every middleware on the HTTP side.
// They are all standard func(http.Handler) http.Handler, so they compose with anything in the net/http ecosystem.
package middleware

import (
	"net/http"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/reqctx"
	"github.com/nthw-dev/user-management-api/internal/platform/id"
)

const RequestIDHeader = "X-Request-ID"

// maxInboundRequestID stops a caller from stuffing in a very long value that would then bloat every line of the log.
const maxInboundRequestID = 64

// RequestID has to be first in the chain, because every remaining layer uses it as a reference.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get(RequestIDHeader)
		if !validInboundID(rid) {
			rid = id.NewULID()
		}

		w.Header().Set(RequestIDHeader, rid)
		next.ServeHTTP(w, r.WithContext(reqctx.WithRequestID(r.Context(), rid)))
	})
}

// validInboundID accepts a value sent by an upstream system so a trace can be followed across services,
// but only characters that are safe to write into a log and to put back into a header.
func validInboundID(v string) bool {
	if v == "" || len(v) > maxInboundRequestID {
		return false
	}
	for _, c := range v {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}
