package handler_test

import (
	"context"
	"testing"
	"time"

	"github.com/saitddundar/vinctum-core/internal/auth"
	identityv1 "github.com/saitddundar/vinctum-core/proto/identity/v1"
	"github.com/saitddundar/vinctum-core/services/identity/handler"
	"github.com/saitddundar/vinctum-core/services/identity/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newTestHandler() *handler.IdentityHandler {
	repo := repository.NewInMemoryUserRepository()
	jwt := auth.NewManager("test-secret", time.Hour, 7*24*time.Hour)
	return handler.NewIdentityHandler(repo, jwt, 4)
}

func TestRegister(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		resp, err := h.Register(ctx, &identityv1.RegisterRequest{
			Username: "alice",
			Email:    "alice@example.com",
			Password: "secret123",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.UserId)
		assert.Equal(t, "alice@example.com", resp.Email)
	})

	t.Run("duplicate email", func(t *testing.T) {
		_, err := h.Register(ctx, &identityv1.RegisterRequest{
			Username: "alice2",
			Email:    "alice@example.com",
			Password: "secret123",
		})
		require.Error(t, err)
		assert.Equal(t, codes.AlreadyExists, status.Code(err))
	})

	t.Run("missing fields", func(t *testing.T) {
		_, err := h.Register(ctx, &identityv1.RegisterRequest{Email: "x@x.com"})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestLogin(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	_, err := h.Register(ctx, &identityv1.RegisterRequest{
		Username: "bob",
		Email:    "bob@example.com",
		Password: "pass1234",
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		resp, err := h.Login(ctx, &identityv1.LoginRequest{
			Email:    "bob@example.com",
			Password: "pass1234",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.AccessToken)
		assert.NotEmpty(t, resp.RefreshToken)
		assert.Equal(t, "bob@example.com", resp.User.Email)
	})

	t.Run("wrong password", func(t *testing.T) {
		_, err := h.Login(ctx, &identityv1.LoginRequest{
			Email:    "bob@example.com",
			Password: "wrong",
		})
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})

	t.Run("unknown email", func(t *testing.T) {
		_, err := h.Login(ctx, &identityv1.LoginRequest{
			Email:    "nobody@example.com",
			Password: "pass1234",
		})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestValidateToken(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	_, err := h.Register(ctx, &identityv1.RegisterRequest{
		Username: "carol",
		Email:    "carol@example.com",
		Password: "pass1234",
	})
	require.NoError(t, err)

	loginResp, err := h.Login(ctx, &identityv1.LoginRequest{
		Email:    "carol@example.com",
		Password: "pass1234",
	})
	require.NoError(t, err)

	t.Run("valid token", func(t *testing.T) {
		resp, err := h.ValidateToken(ctx, &identityv1.ValidateTokenRequest{Token: loginResp.AccessToken})
		require.NoError(t, err)
		assert.True(t, resp.Valid)
		assert.Equal(t, "carol@example.com", resp.Email)
	})

	t.Run("invalid token", func(t *testing.T) {
		resp, err := h.ValidateToken(ctx, &identityv1.ValidateTokenRequest{Token: "garbage"})
		require.NoError(t, err)
		assert.False(t, resp.Valid)
	})
}

func TestRefreshToken(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	_, err := h.Register(ctx, &identityv1.RegisterRequest{
		Username: "dave",
		Email:    "dave@example.com",
		Password: "pass1234",
	})
	require.NoError(t, err)

	loginResp, err := h.Login(ctx, &identityv1.LoginRequest{
		Email:    "dave@example.com",
		Password: "pass1234",
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		resp, err := h.RefreshToken(ctx, &identityv1.RefreshTokenRequest{RefreshToken: loginResp.RefreshToken})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.AccessToken)
	})

	t.Run("invalid refresh token", func(t *testing.T) {
		_, err := h.RefreshToken(ctx, &identityv1.RefreshTokenRequest{RefreshToken: "bad"})
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})
}
