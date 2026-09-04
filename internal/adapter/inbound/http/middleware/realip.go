package middleware

import (
	"net"
	"net/http"
	"strings"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/reqctx"
)

// RealIP finds the caller's true IP when a proxy sits in between.
//
// Caveat: header values can be forged. Without a trusted proxy in front,
// a real system should configure a list of trusted proxies and only believe the header when it comes from that list.
func RealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(reqctx.WithRealIP(r.Context(), clientIP(r))))
	})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// The very first entry is the original client; the rest are the proxies traversed, in order.
		if first, _, found := strings.Cut(xff, ","); found || first != "" {
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
