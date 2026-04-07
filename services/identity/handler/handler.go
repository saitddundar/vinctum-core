package handler

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/internal/auth"
	"github.com/saitddundar/vinctum-core/pkg/crypto"
	"github.com/saitddundar/vinctum-core/pkg/mailer"
	identityv1 "github.com/saitddundar/vinctum-core/proto/identity/v1"
	"github.com/saitddundar/vinctum-core/services/identity/repository"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type IdentityHandler struct {
	identityv1.UnimplementedIdentityServiceServer
	queries    repository.Querier
	jwt        *auth.Manager
	blacklist  *auth.TokenBlacklist
	mailer     *mailer.Mailer
	bcryptCost int
}

func NewIdentityHandler(q repository.Querier, jwt *auth.Manager, bl *auth.TokenBlacklist, m *mailer.Mailer, bcryptCost int) *IdentityHandler {
	return &IdentityHandler{queries: q, jwt: jwt, blacklist: bl, mailer: m, bcryptCost: bcryptCost}
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

	// Generate verification token and send email
	token, err := crypto.GenerateHexToken(32)
	if err != nil {
		log.Error().Err(err).Str("user_id", user.ID).Msg("failed to generate verification token")
	} else {
		expiresAt := time.Now().Add(24 * time.Hour)
		err = h.queries.SetVerificationToken(ctx, repository.SetVerificationTokenParams{
			Column1: user.ID,
			VerificationToken: pgtype.Text{
				String: token,
				Valid:  true,
			},
			VerificationExpiresAt: pgtype.Timestamptz{
				Time:  expiresAt,
				Valid: true,
			},
		})
		if err != nil {
			log.Error().Err(err).Str("user_id", user.ID).Msg("failed to set verification token")
		} else if h.mailer != nil {
			if err := h.mailer.SendVerification(user.Email, token); err != nil {
				log.Error().Err(err).Str("email", user.Email).Msg("failed to send verification email")
			}
		}
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

	if !user.EmailVerified {
		return nil, status.Error(codes.PermissionDenied, "email not verified, please check your inbox")
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
			UserId:        user.ID,
			Username:      user.Username,
			Email:         user.Email,
			CreatedAt:     timestamppb.New(user.CreatedAt),
			EmailVerified: user.EmailVerified,
		},
	}, nil
}

func (h *IdentityHandler) VerifyEmail(ctx context.Context, req *identityv1.VerifyEmailRequest) (*identityv1.VerifyEmailResponse, error) {
	if req.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}

	user, err := h.queries.GetUserByVerificationToken(ctx, pgtype.Text{
		String: req.Token,
		Valid:  true,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &identityv1.VerifyEmailResponse{
				Success: false,
				Message: "invalid or expired verification token",
			}, nil
		}
		return nil, status.Error(codes.Internal, "failed to verify token")
	}

	if user.EmailVerified {
		return &identityv1.VerifyEmailResponse{
			Success: true,
			Message: "email already verified",
		}, nil
	}

	if err := h.queries.VerifyUserEmail(ctx, user.ID); err != nil {
		return nil, status.Error(codes.Internal, "failed to verify email")
	}

	log.Info().Str("user_id", user.ID).Str("email", user.Email).Msg("email verified")

	return &identityv1.VerifyEmailResponse{
		Success: true,
		Message: "email verified successfully",
	}, nil
}

func (h *IdentityHandler) ResendVerification(ctx context.Context, req *identityv1.ResendVerificationRequest) (*identityv1.ResendVerificationResponse, error) {
	if req.Email == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}

	user, err := h.queries.GetUserByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Don't reveal if email exists or not
			return &identityv1.ResendVerificationResponse{
				Success: true,
				Message: "if the email exists, a verification link has been sent",
			}, nil
		}
		return nil, status.Error(codes.Internal, "failed to fetch user")
	}

	if user.EmailVerified {
		return &identityv1.ResendVerificationResponse{
			Success: true,
			Message: "email is already verified",
		}, nil
	}

	token, err := crypto.GenerateHexToken(32)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate token")
	}

	expiresAt := time.Now().Add(24 * time.Hour)
	err = h.queries.SetVerificationToken(ctx, repository.SetVerificationTokenParams{
		Column1: user.ID,
		VerificationToken: pgtype.Text{
			String: token,
			Valid:  true,
		},
		VerificationExpiresAt: pgtype.Timestamptz{
			Time:  expiresAt,
			Valid: true,
		},
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to set verification token")
	}

	if h.mailer != nil {
		if err := h.mailer.SendVerification(user.Email, token); err != nil {
			log.Error().Err(err).Str("email", user.Email).Msg("failed to resend verification email")
			return nil, status.Error(codes.Internal, "failed to send email")
		}
	}

	log.Info().Str("email", user.Email).Msg("verification email resent")

	return &identityv1.ResendVerificationResponse{
		Success: true,
		Message: "verification email sent",
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

	blacklisted, _ := h.blacklist.IsBlacklisted(ctx, req.RefreshToken)
	if blacklisted {
		return nil, status.Error(codes.Unauthenticated, "refresh token has been revoked")
	}

	claims, err := h.jwt.Validate(req.RefreshToken)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid or expired refresh token")
	}

	// Rotate: blacklist the old refresh token so it cannot be reused.
	ttl := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time)
	if err := h.blacklist.Add(ctx, req.RefreshToken, ttl); err != nil {
		log.Warn().Err(err).Msg("failed to blacklist old refresh token")
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
