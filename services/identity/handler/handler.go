package handler

import (
	"context"

	"github.com/rs/zerolog/log"
	identityv1 "github.com/saitddundar/vinctum-core/proto/identity/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type IdentityHandler struct {
	identityv1.UnimplementedIdentityServiceServer
}

func NewIdentityHandler() *IdentityHandler {
	return &IdentityHandler{}
}

func (h *IdentityHandler) Register(ctx context.Context, req *identityv1.RegisterRequest) (*identityv1.RegisterResponse, error) {
	log.Info().Str("email", req.Email).Msg("register request")

	if req.Email == "" || req.Password == "" || req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "email, username and password are required")
	}

	// TODO: persist to database, hash password via pkg/crypto
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

func (h *IdentityHandler) Login(ctx context.Context, req *identityv1.LoginRequest) (*identityv1.LoginResponse, error) {
	log.Info().Str("email", req.Email).Msg("login request")

	if req.Email == "" || req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "email and password are required")
	}

	// TODO: verify credentials, issue JWT
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

func (h *IdentityHandler) ValidateToken(ctx context.Context, req *identityv1.ValidateTokenRequest) (*identityv1.ValidateTokenResponse, error) {
	if req.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}

	// TODO: parse and verify JWT
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

func (h *IdentityHandler) RefreshToken(ctx context.Context, req *identityv1.RefreshTokenRequest) (*identityv1.RefreshTokenResponse, error) {
	if req.RefreshToken == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token is required")
	}

	// TODO: validate refresh token, issue new pair
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

func (h *IdentityHandler) Logout(ctx context.Context, req *identityv1.LogoutRequest) (*identityv1.LogoutResponse, error) {
	// TODO: invalidate refresh token
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}
