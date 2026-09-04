package httpapi

import (
	"time"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/bearer"
	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// ---- request DTO ----

// createUserRequest is the body of both POST /auth/register and POST /users — signing up and creating are the same data;
// only the access control differs, and that is a routing concern.
type createUserRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (r createUserRequest) toInput() app.CreateUserInput {
	return app.CreateUserInput{Name: r.Name, Email: r.Email, Password: r.Password}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (r loginRequest) toInput() app.LoginInput {
	return app.LoginInput{Email: r.Email, Password: r.Password}
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// updateUserRequest uses pointers to tell "this field was not sent" apart from "sent as an empty value",
// the distinction PATCH has to be able to make.
type updateUserRequest struct {
	Name  *string `json:"name"`
	Email *string `json:"email"`
}

func (r updateUserRequest) toInput() app.UpdateUserInput {
	return app.UpdateUserInput{Name: r.Name, Email: r.Email}
}

// ---- response DTO ----

// userResponse is always a separate struct from the entity; an entity is never marshalled out directly,
// so accidentally exposing password_hash cannot happen through an oversight.
type userResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type sessionResponse struct {
	AccessToken  string       `json:"access_token"`
	TokenType    string       `json:"token_type"`
	ExpiresIn    int          `json:"expires_in"`
	RefreshToken string       `json:"refresh_token"`
	User         userResponse `json:"user"`
}

type listMeta struct {
	Limit      int     `json:"limit"`
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

func toUserResponse(u *user.User) userResponse {
	return userResponse{
		ID:        u.ID,
		Name:      u.Name,
		Email:     u.Email.String(),
		CreatedAt: formatTime(u.CreatedAt),
		UpdatedAt: formatTime(u.UpdatedAt),
	}
}

func toSessionResponse(s *app.Session) sessionResponse {
	return sessionResponse{
		AccessToken:  s.AccessToken,
		TokenType:    bearer.Scheme,
		ExpiresIn:    int(s.ExpiresIn.Seconds()),
		RefreshToken: s.RefreshToken,
		User:         toUserResponse(s.User),
	}
}

func toListMeta(page app.Page) listMeta {
	meta := listMeta{Limit: page.Limit, HasMore: page.HasMore()}
	if page.HasMore() {
		next := page.NextCursor
		meta.NextCursor = &next
	}
	return meta
}

// formatTime pins RFC 3339 in UTC with no fractional seconds,
// so the emitted value always keeps its shape rather than varying with whatever resolution the database happened to store.
func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }
