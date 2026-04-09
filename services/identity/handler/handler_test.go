package handler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/saitddundar/vinctum-core/internal/auth"
	identityv1 "github.com/saitddundar/vinctum-core/proto/identity/v1"
	"github.com/saitddundar/vinctum-core/services/identity/handler"
	"github.com/saitddundar/vinctum-core/services/identity/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeQuerier struct {
	users map[string]repository.User
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{users: make(map[string]repository.User)}
}

func (f *fakeQuerier) CreateUser(_ context.Context, arg repository.CreateUserParams) (repository.User, error) {
	for _, u := range f.users {
		if u.Email == arg.Email {
			return repository.User{}, errors.New("duplicate email")
		}
	}
	u := repository.User{
		ID:           "id-" + arg.Email,
		Username:     arg.Username,
		Email:        arg.Email,
		PasswordHash: arg.PasswordHash,
		CreatedAt:    time.Now(),
	}
	f.users[u.ID] = u
	return u, nil
}

func (f *fakeQuerier) GetUserByEmail(_ context.Context, email string) (repository.User, error) {
	for _, u := range f.users {
		if u.Email == email {
			return u, nil
		}
	}
	return repository.User{}, pgx.ErrNoRows
}

func (f *fakeQuerier) GetUserByID(_ context.Context, id string) (repository.User, error) {
	u, ok := f.users[id]
	if !ok {
		return repository.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func newTestHandler() *handler.IdentityHandler {
	jwt, _ := auth.NewManager("test-secret-must-be-at-least-32-chars!!", time.Hour, 7*24*time.Hour)
	bl := auth.NewTokenBlacklist("localhost:6379")
	return handler.NewIdentityHandler(newFakeQuerier(), jwt, bl, 4)
}

func register(t *testing.T, h *handler.IdentityHandler, username, email, password string) {
	t.Helper()
	_, err := h.Register(context.Background(), &identityv1.RegisterRequest{
		Username: username, Email: email, Password: password,
	})
	require.NoError(t, err)
}

func login(t *testing.T, h *handler.IdentityHandler, email, password string) *identityv1.LoginResponse {
	t.Helper()
	resp, err := h.Login(context.Background(), &identityv1.LoginRequest{Email: email, Password: password})
	require.NoError(t, err)
	return resp
}

func TestRegister(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		resp, err := h.Register(ctx, &identityv1.RegisterRequest{
			Username: "alice", Email: "alice@example.com", Password: "secret123",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.UserId)
		assert.Equal(t, "alice@example.com", resp.Email)
	})

	t.Run("duplicate email", func(t *testing.T) {
		_, err := h.Register(ctx, &identityv1.RegisterRequest{
			Username: "alice2", Email: "alice@example.com", Password: "secret123",
		})
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
	register(t, h, "bob", "bob@example.com", "pass1234")

	t.Run("success", func(t *testing.T) {
		resp := login(t, h, "bob@example.com", "pass1234")
		assert.NotEmpty(t, resp.AccessToken)
		assert.NotEmpty(t, resp.RefreshToken)
		assert.Equal(t, "bob@example.com", resp.User.Email)
	})

	t.Run("wrong password", func(t *testing.T) {
		_, err := h.Login(ctx, &identityv1.LoginRequest{Email: "bob@example.com", Password: "wrong"})
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})

	t.Run("unknown email", func(t *testing.T) {
		_, err := h.Login(ctx, &identityv1.LoginRequest{Email: "nobody@example.com", Password: "pass"})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestValidateToken(t *testing.T) {
	h := newTestHandler()
	register(t, h, "carol", "carol@example.com", "pass1234")
	resp := login(t, h, "carol@example.com", "pass1234")

	t.Run("valid token", func(t *testing.T) {
		vr, err := h.ValidateToken(context.Background(), &identityv1.ValidateTokenRequest{Token: resp.AccessToken})
		require.NoError(t, err)
		assert.True(t, vr.Valid)
		assert.Equal(t, "carol@example.com", vr.Email)
	})

	t.Run("invalid token", func(t *testing.T) {
		vr, err := h.ValidateToken(context.Background(), &identityv1.ValidateTokenRequest{Token: "garbage"})
		require.NoError(t, err)
		assert.False(t, vr.Valid)
	})
}

func TestRefreshToken(t *testing.T) {
	h := newTestHandler()
	register(t, h, "dave", "dave@example.com", "pass1234")
	resp := login(t, h, "dave@example.com", "pass1234")

	t.Run("success", func(t *testing.T) {
		rr, err := h.RefreshToken(context.Background(), &identityv1.RefreshTokenRequest{RefreshToken: resp.RefreshToken})
		require.NoError(t, err)
		assert.NotEmpty(t, rr.AccessToken)
	})

	t.Run("invalid refresh token", func(t *testing.T) {
		_, err := h.RefreshToken(context.Background(), &identityv1.RefreshTokenRequest{RefreshToken: "bad"})
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})
}

// Ensure fakeQuerier satisfies the Querier interface at compile time.
var _ repository.Querier = (*fakeQuerier)(nil)
