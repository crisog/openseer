package user

import (
	"context"

	"connectrpc.com/connect"
	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	"github.com/crisog/openseer/internal/app/control-plane/auth/session"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type UserService struct {
	openseerv1connect.UnimplementedUserServiceHandler
	queries *sqlc.Queries
	logger  *zap.Logger
}

func NewUserService(queries *sqlc.Queries, logger *zap.Logger) *UserService {
	return &UserService{
		queries: queries,
		logger:  logger,
	}
}

func (s *UserService) GetUserProfile(
	ctx context.Context,
	req *connect.Request[openseerv1.GetUserProfileRequest],
) (*connect.Response[openseerv1.GetUserProfileResponse], error) {
	user, ok := session.GetUserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	protoUser := &openseerv1.User{
		Id:        user.ID,
		Email:     user.Email,
		Name:      user.Name,
		CreatedAt: timestamppb.New(user.CreatedAt),
		UpdatedAt: timestamppb.New(user.UpdatedAt),
	}

	if user.Image != nil && *user.Image != "" {
		protoUser.Image = user.Image
	}

	return connect.NewResponse(&openseerv1.GetUserProfileResponse{
		Message: "Successfully authenticated with Better Auth session",
		User:    protoUser,
	}), nil
}
