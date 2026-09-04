package grpcapi

import (
	"context"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/actor"
	userv1 "github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpc/gen/user/v1"
	"github.com/nthw-dev/user-management-api/internal/app"
)

// userService turns protobuf into use-case input and turns the result back — nothing else.
// Errors are returned as they come; errorsUnary translates them into a status once, for every method.
type userService struct {
	userv1.UnimplementedUserServiceServer

	users app.UserUseCase
}

func (s *userService) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
	u, err := s.users.Create(ctx, toCreateInput(req))
	if err != nil {
		return nil, err
	}
	return &userv1.CreateUserResponse{User: toProto(u)}, nil
}

func (s *userService) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	u, err := s.users.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &userv1.GetUserResponse{User: toProto(u)}, nil
}

func (s *userService) ListUsers(ctx context.Context, req *userv1.ListUsersRequest) (*userv1.ListUsersResponse, error) {
	page, err := s.users.List(ctx, toListFilter(req))
	if err != nil {
		return nil, err
	}
	return toListProto(page), nil
}

// UpdateUser edits only the fields that were sent; a field that was not sent is left untouched — like PATCH on the REST side.
func (s *userService) UpdateUser(ctx context.Context, req *userv1.UpdateUserRequest) (*userv1.UpdateUserResponse, error) {
	u, err := s.users.Update(ctx, actor.ID(ctx), req.GetId(), app.UpdateUserInput{
		Name:  req.Name,
		Email: req.Email,
	})
	if err != nil {
		return nil, err
	}
	return &userv1.UpdateUserResponse{User: toProto(u)}, nil
}

// DeleteUser yields NotFound on a repeated delete, because that row really is gone — the same reasoning as the 404 on the REST side.
func (s *userService) DeleteUser(ctx context.Context, req *userv1.DeleteUserRequest) (*userv1.DeleteUserResponse, error) {
	if err := s.users.Delete(ctx, actor.ID(ctx), req.GetId()); err != nil {
		return nil, err
	}
	return &userv1.DeleteUserResponse{}, nil
}
