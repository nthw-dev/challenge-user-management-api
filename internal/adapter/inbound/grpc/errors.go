package grpcapi

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/apierr"
)

// errorDomain is the ErrorInfo.domain every failure carries — the service that produced the reason.
const errorDomain = "user-service"

// codeOf is the whole of this transport's error table: one gRPC code per shared code — the twin of respond's statusOf.
var codeOf = map[apierr.Code]codes.Code{
	apierr.CodeValidation:         codes.InvalidArgument,
	apierr.CodeUserNotFound:       codes.NotFound,
	apierr.CodeEmailTaken:         codes.AlreadyExists,
	apierr.CodeInvalidCredentials: codes.Unauthenticated,
	apierr.CodeUnauthorized:       codes.Unauthenticated,
	apierr.CodeForbidden:          codes.PermissionDenied,
	apierr.CodeInternal:           codes.Internal,
}

// errorsUnary is the counterpart of respond.Error: every error a method or an inner interceptor returns passes through here once,
// is translated into a status, and — if it is something we did not expect — is logged with enough context to find it again.
func errorsUnary(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		if _, already := status.FromError(err); already {
			// Something inner has already spoken gRPC (a recovered panic) — nothing to translate.
			return resp, err
		}
		// The context ran out — the server ceiling from timeoutUnary, or the caller's own deadline — before the handler
		// finished. That is not an internal fault, and the caller should be told which it was in gRPC's own words.
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, status.Error(codes.DeadlineExceeded, "the call did not finish within its deadline")
		case errors.Is(err, context.Canceled):
			return nil, status.Error(codes.Canceled, "the call was cancelled")
		}

		st := toStatus(err, requestID(ctx))
		if st.Code() == codes.Internal {
			// The details stay in the server-side log only; the answer reveals nothing.
			log.LogAttrs(ctx, slog.LevelError, "grpc_unhandled_error",
				slog.String("method", info.FullMethod),
				slog.String("request_id", requestID(ctx)),
				slog.Any("err", err),
			)
		}
		return nil, st.Err()
	}
}

// toStatus maps the shared vocabulary onto a gRPC status carrying the same three things the REST error body carries:
// the stable code (as google.rpc.ErrorInfo.reason, since a gRPC code alone is coarser — two shared codes both become
// Unauthenticated), the request id to quote (in ErrorInfo.metadata), and, for a validation failure, the field-level
// detail as google.rpc.BadRequest — so a client can read which field failed by machine, as on the REST side.
func toStatus(err error, requestID string) *status.Status {
	p := apierr.Classify(err)

	code, known := codeOf[p.Code]
	if !known {
		code = codes.Internal
	}
	info := &errdetails.ErrorInfo{Reason: string(p.Code), Domain: errorDomain}
	if requestID != "" {
		info.Metadata = map[string]string{"request_id": requestID}
	}
	details := []protoadapt.MessageV1{info}

	if len(p.Fields) > 0 {
		br := &errdetails.BadRequest{}
		for _, f := range p.Fields {
			br.FieldViolations = append(br.FieldViolations, &errdetails.BadRequest_FieldViolation{
				Field:       f.Field,
				Description: f.Issue,
			})
		}
		details = append(details, br)
	}

	st := status.New(code, p.Message)
	if withDetails, err := st.WithDetails(details...); err == nil {
		return withDetails
	}
	return st
}
