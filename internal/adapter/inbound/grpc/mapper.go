package grpcapi

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/bearer"
	userv1 "github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpc/gen/user/v1"
	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// toProto turns an entity into a protobuf message.
// The contract on this side has no password field, so it cannot be sent out by accident, just as on the REST side.
// Timestamps are cut to whole seconds, the same precision REST emits, so the two transports never disagree about one row.
func toProto(u *user.User) *userv1.User {
	return &userv1.User{
		Id:        u.ID,
		Name:      u.Name,
		Email:     u.Email.String(),
		CreatedAt: timestamppb.New(u.CreatedAt.UTC().Truncate(time.Second)),
		UpdatedAt: timestamppb.New(u.UpdatedAt.UTC().Truncate(time.Second)),
	}
}

// credentials is what CreateUserRequest and RegisterRequest have in common — the same three fields, for the same use case.
type credentials interface {
	GetName() string
	GetEmail() string
	GetPassword() string
}

func toCreateInput(r credentials) app.CreateUserInput {
	return app.CreateUserInput{Name: r.GetName(), Email: r.GetEmail(), Password: r.GetPassword()}
}

// toListFilter pulls the filters out of the request and leaves it to the use case to judge whether the values are usable.
// limit is optional in the contract, so "not sent" and "sent as zero" arrive as different things — and are forwarded as such.
func toListFilter(req *userv1.ListUsersRequest) app.ListFilter {
	f := app.ListFilter{Cursor: req.GetCursor(), Query: req.GetQuery()}
	if req.Limit != nil {
		n := int(*req.Limit)
		f.Limit = &n
	}
	return f
}

// toListProto assembles a single page of users, with the same meta set as listMeta on the REST side.
func toListProto(page app.Page) *userv1.ListUsersResponse {
	items := make([]*userv1.User, 0, len(page.Users))
	for i := range page.Users {
		items = append(items, toProto(&page.Users[i]))
	}

	meta := &userv1.ListMeta{
		Limit:   int32(page.Limit), //nolint:gosec // the use case caps it at MaxListLimit, so it cannot overflow int32
		HasMore: page.HasMore(),
	}
	if page.HasMore() {
		next := page.NextCursor
		meta.NextCursor = &next
	}
	return &userv1.ListUsersResponse{Users: items, Meta: meta}
}

// toSessionProto is the one place a session becomes a message — Login and Refresh both return it, as the contract says.
// Its shape matches sessionResponse on the REST side one for one, so the two transports tell the same story.
func toSessionProto(s *app.Session) *userv1.Session {
	return &userv1.Session{
		AccessToken:  s.AccessToken,
		TokenType:    bearer.Scheme,
		ExpiresIn:    int64(s.ExpiresIn.Seconds()),
		RefreshToken: s.RefreshToken,
		User:         toProto(s.User),
	}
}
