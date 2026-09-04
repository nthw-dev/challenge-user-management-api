package middleware

import "net/http"

// MaxBytes guards against an enormous payload being sent in to burn memory.
// Once the limit is passed, MaxBytesReader makes the decoder return an error, which is translated into a 400 at the handler layer.
func MaxBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}
