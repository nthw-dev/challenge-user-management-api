package grpcapi

import (
	"context"

	userv1 "github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpc/gen/user/v1"
	"github.com/nthw-dev/user-management-api/internal/app"
)

// authService stands on the very same AuthUseCase that /api/v1/auth/login uses,
// so logging in over gRPC gets every rule identically, including the uniform answer when authentication fails.
type authService struct {
	userv1.UnimplementedAuthServiceServer

	users app.UserUseCase
	auth  app.AuthUseCase
}

// Register creates a user without requiring a token — it is the way in for the very first user, before anyone has issued them one.
// Unlike UserService.CreateUser, which does the same job but demands authentication first.
//
// Signing up returns no token straight away, exactly as on the REST side — once signed up, you must call Login next.
func (s *authService) Register(ctx context.Context, req *userv1.RegisterRequest) (*userv1.RegisterResponse, error) {
	u, err := s.users.Create(ctx, toCreateInput(req))
	if err != nil {
		return nil, err
	}
	return &userv1.RegisterResponse{User: toProto(u)}, nil
}

// Login exchanges an email and password for the token UserService requires — see publicPrefixes in interceptor.go.
func (s *authService) Login(ctx context.Context, req *userv1.LoginRequest) (*userv1.LoginResponse, error) {
	session, err := s.auth.Login(ctx, app.LoginInput{
		Email:    req.GetEmail(),
		Password: req.GetPassword(),
	})
	if err != nil {
		return nil, err
	}
	return &userv1.LoginResponse{Session: toSessionProto(session)}, nil
}

// Refresh rotates a refresh token — every rotation invalidates the old one,
// and presenting an already-rotated token wipes every session belonging to that user. That rule lives in the application layer.
func (s *authService) Refresh(ctx context.Context, req *userv1.RefreshRequest) (*userv1.RefreshResponse, error) {
	session, err := s.auth.Refresh(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, err
	}
	return &userv1.RefreshResponse{Session: toSessionProto(session)}, nil
}
