//go:build dev

package grpcapi

import (
	"strings"

	"google.golang.org/protobuf/proto"

	userv1 "github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpc/gen/user/v1"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpcconsole"
)

// ConsoleGuide is the guide that appears above /grpcui/'s form.
//
// The content lives here rather than in the grpcconsole package, because what needs explaining is this adapter's contract.
// grpcconsole knows only how to render a Guide; it does not know what UserService requires you to attach.
//
// The examples are ordered so they can be clicked top to bottom, in the order the work actually happens.
func ConsoleGuide() grpcconsole.Guide {
	return grpcconsole.Guide{
		Title: "Authorization",
		Intro: "`user.v1.UserService/*` requires a token; `user.v1.AuthService/*`, `grpc.health.v1.Health/Check` " +
			"and reflection do not (gRPC has no `Authorization` header, only metadata, which serves the same purpose).",
		Steps: []grpcconsole.GuideStep{
			{
				Body: "`AuthService/Register` (if no user exists yet) → `AuthService/Login` → copy `session.access_token`",
			},
			{
				Body: "Under the Request Metadata heading, set the `authorization` row to `Bearer <access_token>` — " +
					"the `Bearer ` part is prefilled, so just paste the token after it. The name must be lowercase, as HTTP/2 requires.",
			},
			{
				Body: "Once it expires per `JWT_ACCESS_TTL`, call `AuthService/Refresh` " +
					"(the old token stops working; reusing it wipes every session) and paste the new value.",
			},
		},
		Notes: []string{
			"The same contract can be called over REST at /swagger/ — there you attach an `Authorization` header instead of metadata.",
		},
		// The metadata name is prefilled as soon as the page opens, leaving only the copied value to paste.
		DefaultMetadata: []string{authMetadataKey + ": Bearer "},
		Examples: []grpcconsole.GuideExample{
			exampleFor(userv1.AuthService_Register_FullMethodName, grpcconsole.GuideExample{
				Name:        "1. Register (no token needed)",
				Description: "The way in for the very first user; returns no token",
				Data: &userv1.RegisterRequest{
					Name:     "First User",
					Email:    "natthawat@example.com",
					Password: "Str0ng-Passw0rd!",
				},
			}),
			exampleFor(userv1.AuthService_Login_FullMethodName, grpcconsole.GuideExample{
				Name:        "2. Login (no token needed)",
				Description: "Change this to a user that really exists, then take session.access_token from the Response",
				Data: &userv1.LoginRequest{
					Email:    "natthawat@example.com",
					Password: "Str0ng-Passw0rd!",
				},
			}),
			exampleFor(userv1.UserService_CreateUser_FullMethodName, withToken(grpcconsole.GuideExample{
				Name:        "3. CreateUser",
				Description: "The same job as Register, but requires a token — equivalent to POST /api/v1/users",
				Data: &userv1.CreateUserRequest{
					Name:     "New User",
					Email:    "new.user@example.com",
					Password: "Str0ng-Passw0rd!",
				},
			})),
			exampleFor(userv1.UserService_ListUsers_FullMethodName, withToken(grpcconsole.GuideExample{
				Name:        "4. ListUsers",
				Description: "Paged by cursor — put meta.next_cursor into the cursor field on the following round",
				Data:        &userv1.ListUsersRequest{Limit: proto.Int32(20)},
			})),
			exampleFor(userv1.UserService_GetUser_FullMethodName, withToken(grpcconsole.GuideExample{
				Name:        "5. GetUser",
				Description: "Replace id with a value from Register, CreateUser or ListUsers",
				Data:        &userv1.GetUserRequest{Id: idPlaceholder},
			})),
			exampleFor(userv1.UserService_UpdateUser_FullMethodName, withToken(grpcconsole.GuideExample{
				Name:        "6. UpdateUser",
				Description: "A field that is not sent is left untouched; feel free to delete it from the form",
				Data: &userv1.UpdateUserRequest{
					Id:   idPlaceholder,
					Name: proto.String("New Name"),
				},
			})),
			exampleFor(userv1.UserService_DeleteUser_FullMethodName, withToken(grpcconsole.GuideExample{
				Name:        "7. DeleteUser",
				Description: "A repeated delete yields NotFound, because that row really is gone",
				Data:        &userv1.DeleteUserRequest{Id: idPlaceholder},
			})),
			exampleFor(userv1.AuthService_Refresh_FullMethodName, grpcconsole.GuideExample{
				Name:        "8. Refresh (no token needed)",
				Description: "Supply the refresh_token from Login — rotating returns a new pair, and the old one stops working",
				Data:        &userv1.RefreshRequest{RefreshToken: "paste the refresh_token here"}, //nolint:gosec // a placeholder shown in the console, not a credential
			}),
		},
	}
}

// idPlaceholder is a value that must be replaced before clicking Invoke — deliberately written so it reads as not being a real id.
const idPlaceholder = "paste the user's id here"

// withToken marks an example as requiring authentication, so the authorization row is prefilled and waiting for a value.
// It is a function so that every token-requiring example fills in exactly the same row, rather than each typing its own.
func withToken(ex grpcconsole.GuideExample) grpcconsole.GuideExample {
	ex.Metadata = []string{authMetadataKey + ": Bearer "}
	return ex
}

// exampleFor fills in the service and method names from the constants protoc generates.
// These two go straight into the form's dropdowns; a typo means clicking the example does nothing.
// Referencing the constants means there is no hand-typed name to misspell, and renaming in the proto file breaks the compile immediately.
func exampleFor(fullMethod string, ex grpcconsole.GuideExample) grpcconsole.GuideExample {
	ex.Service, ex.Method, _ = strings.Cut(strings.TrimPrefix(fullMethod, "/"), "/")
	return ex
}
