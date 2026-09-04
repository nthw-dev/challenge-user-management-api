// Package reqctx keeps the values bound to a single request inside the context.
//
// It is a package of its own because both the middleware and the response writer need it,
// and if it lived in either one, the other would import back into it in a cycle.
package reqctx

import "context"

type ctxKey struct{ name string }

var (
	requestIDKey = ctxKey{"request_id"}
	realIPKey    = ctxKey{"real_ip"}
)

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func WithRealIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, realIPKey, ip)
}

func RealIP(ctx context.Context) string {
	ip, _ := ctx.Value(realIPKey).(string)
	return ip
}
