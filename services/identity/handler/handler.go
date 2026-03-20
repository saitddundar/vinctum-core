package handler

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/internal/auth"
	"github.com/saitddundar/vinctum-core/pkg/crypto"
	identityv1 "github.com/saitddundar/vinctum-core/proto/identity/v1"
	"github.com/saitddundar/vinctum-core/services/identity/repository"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type IdentityHandler struct {
	identityv1.UnimplementedIdentityServiceServer
	queries    *repository.Queries
	jwt        *auth.Manager
	blacklist  *auth.TokenBlacklist
	bcryptCost int
}

func NewIdentityHandler(q *repository.Queries, jwt *auth.Manager, bl *auth.TokenBlacklist, bcryptCost int) *IdentityHandler {
	return &IdentityHandler{queries: q, jwt: jwt, blacklist: bl, bcryptCost: bcryptCost}
}

func (h *IdentityHandler) Register(ctx context.Context, req *identityv1.RegisterRequest) (*identityv1.RegisterResponse, error) {
	if req.Email == "" || req.Password == "" || req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "email, username and password are required")
	}

	hash, err := crypto.HashPassword(req.Password, h.bcryptCost)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to process password")
	}

	user, err := h.queries.CreateUser(ctx, repository.CreateUserParams{
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: hash,
	})
	if err != nil {
		return nil, status.Error(codes.AlreadyExists, "email already registered")
	}

	log.Info().Str("user_id", user.ID).Str("email", user.Email).Msg("user registered")

	return &identityv1.RegisterResponse{
		UserId:    user.ID,
		Username:  user.Username,
		Email:     user.Email,
		CreatedAt: timestamppb.New(user.CreatedAt),
	}, nil
}

func (h *IdentityHandler) Login(ctx context.Context, req *identityv1.LoginRequest) (*identityv1.LoginResponse, error) {
	if req.Email == "" || req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "email and password are required")
	}

	user, err := h.queries.GetUserByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "invalid credentials")
		}
		return nil, status.Error(codes.Internal, "failed to fetch user")
	}

	if err := crypto.CheckPassword(req.Password, user.PasswordHash); err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	pair, err := h.jwt.Issue(user.ID, user.Email)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to issue token")
	}

	log.Info().Str("user_id", user.ID).Msg("user logged in")

	return &identityv1.LoginResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		User: &identityv1.User{
			UserId:    user.ID,
			Username:  user.Username,
			Email:     user.Email,
			CreatedAt: timestamppb.New(user.CreatedAt),
		},
	}, nil
}

func (h *IdentityHandler) ValidateToken(ctx context.Context, req *identityv1.ValidateTokenRequest) (*identityv1.ValidateTokenResponse, error) {
	if req.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}

	blacklisted, _ := h.blacklist.IsBlacklisted(ctx, req.Token)
	if blacklisted {
		return &identityv1.ValidateTokenResponse{Valid: false}, nil
	}

	claims, err := h.jwt.Validate(req.Token)
	if err != nil {
		return &identityv1.ValidateTokenResponse{Valid: false}, nil
	}

	return &identityv1.ValidateTokenResponse{
		Valid:  true,
		UserId: claims.Subject,
		Email:  claims.Email,
	}, nil
}

func (h *IdentityHandler) RefreshToken(ctx context.Context, req *identityv1.RefreshTokenRequest) (*identityv1.RefreshTokenResponse, error) {
	if req.RefreshToken == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token is required")
	}

	claims, err := h.jwt.Validate(req.RefreshToken)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid or expired refresh token")
	}

	pair, err := h.jwt.Issue(claims.Subject, claims.Email)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to issue token")
	}

	return &identityv1.RefreshTokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
	}, nil
}

func (h *IdentityHandler) Logout(ctx context.Context, req *identityv1.LogoutRequest) (*identityv1.LogoutResponse, error) {
	if req.RefreshToken == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token is required")
	}

	claims, err := h.jwt.Validate(req.RefreshToken)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}

	ttl := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time)
	if err := h.blacklist.Add(ctx, req.RefreshToken, ttl); err != nil {
		return nil, status.Error(codes.Internal, "failed to invalidate token")
	}

	return &identityv1.LogoutResponse{Success: true}, nil
}
