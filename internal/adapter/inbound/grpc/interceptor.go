package grpcapi

import (
	"context"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/actor"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/apierr"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/bearer"
	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/platform/id"
)

// authMetadataKey is the metadata name authUnary reads — declared in one place so the console guide and the code cannot disagree.
const authMetadataKey = "authorization"

// publicPrefixes are the methods that need no authentication — health checks, reflection and logging in.
//
// AuthService is in here as a whole contract, for the same reason /api/v1/auth/* on the REST side
// sits outside Authenticate: it is where the token is issued, so it cannot be made to require one first.
// A test checks this list against the generated service descriptors, so adding an RPC cannot silently leave it out.
var publicPrefixes = []string{
	"/grpc.health.v1.Health/",
	"/grpc.reflection.",
	"/user.v1.AuthService/",
}

func isPublicMethod(full string) bool {
	for _, p := range publicPrefixes {
		if strings.HasPrefix(full, p) {
			return true
		}
	}
	return false
}

// authUnary uses the same TokenVerifier the REST side uses.
// gRPC has no Authorization header in the HTTP sense, but it has metadata, which serves the same purpose.
// Every way of failing answers with the same error — which stage failed is not the caller's business.
// The verified subject becomes the call's actor, exactly as Authenticate does on the REST side.
func authUnary(v app.TokenVerifier) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if isPublicMethod(info.FullMethod) {
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, apierr.ErrUnauthenticated
		}
		vals := md.Get(authMetadataKey)
		if len(vals) == 0 {
			return nil, apierr.ErrUnauthenticated
		}
		raw, ok := bearer.Token(vals[0])
		if !ok {
			return nil, apierr.ErrUnauthenticated
		}
		userID, err := v.Verify(raw)
		if err != nil {
			return nil, apierr.ErrUnauthenticated
		}
		return handler(actor.Set(ctx, userID), req)
	}
}

// requestIDMetadataKey is the metadata name the request id travels under, both in and out — the twin of X-Request-ID on the REST side.
const requestIDMetadataKey = "x-request-id"

// loggingUnary writes logs in the same shape as the REST side, so a trace can be followed across transports in one place.
// It settles the request id for everything inside it, so an error log and the access log quote the same one,
// and echoes it back as a response header, exactly as the REST side echoes X-Request-ID.
func loggingUnary(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		rid := requestIDFromMetadata(ctx)
		ctx = context.WithValue(ctx, requestIDKey{}, rid)
		// authUnary runs further in; the slot is how its verified subject reaches this line.
		ctx = actor.Reserve(ctx)
		// Fails only when there is no transport stream behind ctx (a direct call in a test) — nothing to echo to then.
		_ = grpc.SetHeader(ctx, metadata.Pairs(requestIDMetadataKey, rid))

		resp, err := handler(ctx, req)

		attrs := []slog.Attr{
			slog.String("method", info.FullMethod),
			slog.String("code", status.Code(err).String()),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("request_id", rid),
		}
		if id := actor.ID(ctx); id != "" {
			attrs = append(attrs, slog.String("actor_id", id))
		}
		log.LogAttrs(ctx, slog.LevelInfo, "grpc_request", attrs...)
		return resp, err
	}
}

// recoveryUnary keeps a panic in a handler from bringing down the whole process.
func recoveryUnary(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				log.LogAttrs(ctx, slog.LevelError, "grpc_panic_recovered",
					slog.String("method", info.FullMethod),
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
				)
				err = status.Error(codes.Internal, "an internal error occurred")
			}
		}()
		return handler(ctx, req)
	}
}

// timeoutUnary puts a ceiling on every RPC, otherwise a stalled handler would keep eating database connections.
// A caller's own deadline is honored when it is shorter — context.WithTimeout keeps whichever comes first — but
// never when it is longer: the server decides how long it is willing to work, not the client.
func timeoutUnary(d time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return handler(ctx, req)
	}
}

type requestIDKey struct{}

// requestID returns the id loggingUnary settled on, or "" outside a logged call.
func requestID(ctx context.Context) string {
	rid, _ := ctx.Value(requestIDKey{}).(string)
	return rid
}

func requestIDFromMetadata(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(requestIDMetadataKey); len(vals) > 0 && vals[0] != "" {
			return vals[0]
		}
	}
	return id.NewULID()
}
