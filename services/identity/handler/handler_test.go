package handler_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/saitddundar/vinctum-core/internal/auth"
	"github.com/saitddundar/vinctum-core/pkg/middleware"
	identityv1 "github.com/saitddundar/vinctum-core/proto/identity/v1"
	"github.com/saitddundar/vinctum-core/services/identity/handler"
	"github.com/saitddundar/vinctum-core/services/identity/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeQuerier is an in-memory implementation of repository.Querier for handler tests.
type fakeQuerier struct {
	users          map[string]repository.User
	devices        map[string]repository.Device
	deviceKeys     map[string]repository.DeviceKey
	sessions       map[string]repository.PeerSession
	sessionDevices map[string]map[string]bool // session_id -> set of device_id
	nextID         int
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{
		users:          make(map[string]repository.User),
		devices:        make(map[string]repository.Device),
		deviceKeys:     make(map[string]repository.DeviceKey),
		sessions:       make(map[string]repository.PeerSession),
		sessionDevices: make(map[string]map[string]bool),
	}
}

func (f *fakeQuerier) newID(prefix string) string {
	f.nextID++
	return prefix + "-" + strconv.Itoa(f.nextID)
}

// ─── Users ──────────────────────────────────────────

func (f *fakeQuerier) CreateUser(_ context.Context, arg repository.CreateUserParams) (repository.User, error) {
	for _, u := range f.users {
		if u.Email == arg.Email {
			return repository.User{}, errors.New("duplicate email")
		}
	}
	u := repository.User{
		ID:            f.newID("user"),
		Username:      arg.Username,
		Email:         arg.Email,
		PasswordHash:  arg.PasswordHash,
		CreatedAt:     time.Now(),
		EmailVerified: false,
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

func (f *fakeQuerier) SetVerificationToken(_ context.Context, arg repository.SetVerificationTokenParams) error {
	u, ok := f.users[arg.Column1]
	if !ok {
		return pgx.ErrNoRows
	}
	u.VerificationToken = arg.VerificationToken
	u.VerificationExpiresAt = arg.VerificationExpiresAt
	f.users[u.ID] = u
	return nil
}

func (f *fakeQuerier) GetUserByVerificationToken(_ context.Context, token pgtype.Text) (repository.User, error) {
	for _, u := range f.users {
		if u.VerificationToken.Valid && u.VerificationToken.String == token.String {
			return u, nil
		}
	}
	return repository.User{}, pgx.ErrNoRows
}

func (f *fakeQuerier) VerifyUserEmail(_ context.Context, id string) error {
	u, ok := f.users[id]
	if !ok {
		return pgx.ErrNoRows
	}
	u.EmailVerified = true
	u.VerificationToken = pgtype.Text{}
	u.VerificationExpiresAt = pgtype.Timestamptz{}
	f.users[id] = u
	return nil
}

// markEmailVerified is a test helper bypassing the verification flow.
func (f *fakeQuerier) markEmailVerified(email string) {
	for id, u := range f.users {
		if u.Email == email {
			u.EmailVerified = true
			f.users[id] = u
			return
		}
	}
}

// ─── Devices ────────────────────────────────────────

func (f *fakeQuerier) CreateDevice(_ context.Context, arg repository.CreateDeviceParams) (repository.Device, error) {
	d := repository.Device{
		ID:          f.newID("device"),
		UserID:      arg.UserID,
		Name:        arg.Name,
		DeviceType:  arg.DeviceType,
		NodeID:      arg.NodeID,
		Fingerprint: arg.Fingerprint,
		IsApproved:  arg.IsApproved,
		ApprovedAt:  arg.ApprovedAt,
		ApprovedBy:  arg.ApprovedBy,
		LastActive:  time.Now(),
		CreatedAt:   time.Now(),
	}
	f.devices[d.ID] = d
	return d, nil
}

func (f *fakeQuerier) GetDeviceByID(_ context.Context, id string) (repository.Device, error) {
	d, ok := f.devices[id]
	if !ok || d.RevokedAt.Valid {
		return repository.Device{}, pgx.ErrNoRows
	}
	return d, nil
}

func (f *fakeQuerier) GetDeviceByFingerprint(_ context.Context, arg repository.GetDeviceByFingerprintParams) (repository.Device, error) {
	for _, d := range f.devices {
		if d.UserID == arg.Column1 && d.Fingerprint == arg.Fingerprint && !d.RevokedAt.Valid {
			return d, nil
		}
	}
	return repository.Device{}, pgx.ErrNoRows
}

func (f *fakeQuerier) ListDevicesByUser(_ context.Context, userID string) ([]repository.Device, error) {
	var out []repository.Device
	for _, d := range f.devices {
		if d.UserID == userID && !d.RevokedAt.Valid {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeQuerier) RevokeDevice(_ context.Context, arg repository.RevokeDeviceParams) error {
	d, ok := f.devices[arg.Column1]
	if !ok || d.UserID != arg.Column2 {
		return nil
	}
	d.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.devices[d.ID] = d
	return nil
}

func (f *fakeQuerier) ApproveDevice(_ context.Context, arg repository.ApproveDeviceParams) error {
	d, ok := f.devices[arg.Column1]
	if !ok {
		return nil
	}
	d.IsApproved = true
	d.ApprovedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.devices[d.ID] = d
	return nil
}

func (f *fakeQuerier) RejectDevice(_ context.Context, id string) error {
	d, ok := f.devices[id]
	if !ok || d.IsApproved {
		return nil
	}
	d.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.devices[d.ID] = d
	return nil
}

func (f *fakeQuerier) UpdateDeviceActivity(_ context.Context, arg repository.UpdateDeviceActivityParams) error {
	d, ok := f.devices[arg.Column1]
	if !ok || d.RevokedAt.Valid {
		return nil
	}
	d.LastActive = time.Now()
	if arg.NodeID.Valid {
		d.NodeID = arg.NodeID
	}
	f.devices[d.ID] = d
	return nil
}

// ─── Peer Sessions ──────────────────────────────────

func (f *fakeQuerier) CreatePeerSession(_ context.Context, arg repository.CreatePeerSessionParams) (repository.PeerSession, error) {
	s := repository.PeerSession{
		ID:        f.newID("session"),
		UserID:    arg.UserID,
		Name:      arg.Name,
		IsActive:  true,
		CreatedAt: time.Now(),
	}
	f.sessions[s.ID] = s
	return s, nil
}

func (f *fakeQuerier) GetPeerSession(_ context.Context, id string) (repository.PeerSession, error) {
	s, ok := f.sessions[id]
	if !ok {
		return repository.PeerSession{}, pgx.ErrNoRows
	}
	return s, nil
}

func (f *fakeQuerier) ListActivePeerSessions(_ context.Context, userID string) ([]repository.PeerSession, error) {
	var out []repository.PeerSession
	for _, s := range f.sessions {
		if s.UserID == userID && s.IsActive {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeQuerier) ClosePeerSession(_ context.Context, arg repository.ClosePeerSessionParams) error {
	s, ok := f.sessions[arg.Column1]
	if !ok || s.UserID != arg.Column2 {
		return nil
	}
	s.IsActive = false
	s.ClosedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.sessions[s.ID] = s
	return nil
}

func (f *fakeQuerier) AddDeviceToSession(_ context.Context, arg repository.AddDeviceToSessionParams) error {
	if f.sessionDevices[arg.SessionID] == nil {
		f.sessionDevices[arg.SessionID] = make(map[string]bool)
	}
	f.sessionDevices[arg.SessionID][arg.DeviceID] = true
	return nil
}

func (f *fakeQuerier) RemoveDeviceFromSession(_ context.Context, arg repository.RemoveDeviceFromSessionParams) error {
	if m := f.sessionDevices[arg.Column1]; m != nil {
		delete(m, arg.Column2)
	}
	return nil
}

func (f *fakeQuerier) ListSessionDevices(_ context.Context, sessionID string) ([]repository.Device, error) {
	var out []repository.Device
	for deviceID := range f.sessionDevices[sessionID] {
		if d, ok := f.devices[deviceID]; ok && !d.RevokedAt.Valid {
			out = append(out, d)
		}
	}
	return out, nil
}

// ─── Device Keys ────────────────────────────────────

func (f *fakeQuerier) UpsertDeviceKey(_ context.Context, arg repository.UpsertDeviceKeyParams) (repository.DeviceKey, error) {
	existing, exists := f.deviceKeys[arg.Column1]
	k := repository.DeviceKey{
		DeviceID:     arg.Column1,
		KexAlgo:      arg.KexAlgo,
		KexPublicKey: arg.KexPublicKey,
	}
	if exists {
		k.CreatedAt = existing.CreatedAt
		k.RotatedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	} else {
		k.CreatedAt = time.Now()
	}
	f.deviceKeys[arg.Column1] = k
	return k, nil
}

func (f *fakeQuerier) GetDeviceKey(_ context.Context, deviceID string) (repository.DeviceKey, error) {
	k, ok := f.deviceKeys[deviceID]
	if !ok {
		return repository.DeviceKey{}, pgx.ErrNoRows
	}
	return k, nil
}

func (f *fakeQuerier) ListSessionDeviceKeys(_ context.Context, sessionID string) ([]repository.DeviceKey, error) {
	var out []repository.DeviceKey
	for deviceID := range f.sessionDevices[sessionID] {
		d, ok := f.devices[deviceID]
		if !ok || d.RevokedAt.Valid {
			continue
		}
		if k, ok := f.deviceKeys[deviceID]; ok {
			out = append(out, k)
		}
	}
	return out, nil
}

// Ensure fakeQuerier satisfies the Querier interface at compile time.
var _ repository.Querier = (*fakeQuerier)(nil)

// ─── Test helpers ──────────────────────────────────

type testFixture struct {
	q *fakeQuerier
	h *handler.IdentityHandler
}

func newTestFixture() *testFixture {
	q := newFakeQuerier()
	jwt, _ := auth.NewManager("test-secret-must-be-at-least-32-chars!!", time.Hour, 7*24*time.Hour)
	bl := auth.NewTokenBlacklist("localhost:6379")
	ps := auth.NewPairingStore("localhost:6379")
	h := handler.NewIdentityHandler(q, jwt, bl, ps, nil, 4)
	return &testFixture{q: q, h: h}
}

func register(t *testing.T, f *testFixture, username, email, password string) {
	t.Helper()
	_, err := f.h.Register(context.Background(), &identityv1.RegisterRequest{
		Username: username, Email: email, Password: password,
	})
	require.NoError(t, err)
	f.q.markEmailVerified(email)
}

func login(t *testing.T, f *testFixture, email, password string) *identityv1.LoginResponse {
	t.Helper()
	resp, err := f.h.Login(context.Background(), &identityv1.LoginRequest{Email: email, Password: password})
	require.NoError(t, err)
	return resp
}

// authCtx returns a context with a user_id injected so handlers requiring auth pass.
func authCtx(userID string) context.Context {
	return context.WithValue(context.Background(), middleware.UserIDKey, userID)
}

// ─── Tests ──────────────────────────────────────────

func TestRegister(t *testing.T) {
	f := newTestFixture()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		resp, err := f.h.Register(ctx, &identityv1.RegisterRequest{
			Username: "alice", Email: "alice@example.com", Password: "secret123",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.UserId)
		assert.Equal(t, "alice@example.com", resp.Email)
	})

	t.Run("duplicate email", func(t *testing.T) {
		_, err := f.h.Register(ctx, &identityv1.RegisterRequest{
			Username: "alice2", Email: "alice@example.com", Password: "secret123",
		})
		assert.Equal(t, codes.AlreadyExists, status.Code(err))
	})

	t.Run("missing fields", func(t *testing.T) {
		_, err := f.h.Register(ctx, &identityv1.RegisterRequest{Email: "x@x.com"})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestLogin(t *testing.T) {
	f := newTestFixture()
	ctx := context.Background()
	register(t, f, "bob", "bob@example.com", "pass1234")

	t.Run("success", func(t *testing.T) {
		resp := login(t, f, "bob@example.com", "pass1234")
		assert.NotEmpty(t, resp.AccessToken)
		assert.NotEmpty(t, resp.RefreshToken)
		assert.Equal(t, "bob@example.com", resp.User.Email)
	})

	t.Run("wrong password", func(t *testing.T) {
		_, err := f.h.Login(ctx, &identityv1.LoginRequest{Email: "bob@example.com", Password: "wrong"})
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})

	t.Run("unknown email", func(t *testing.T) {
		_, err := f.h.Login(ctx, &identityv1.LoginRequest{Email: "nobody@example.com", Password: "pass"})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestValidateToken(t *testing.T) {
	f := newTestFixture()
	register(t, f, "carol", "carol@example.com", "pass1234")
	resp := login(t, f, "carol@example.com", "pass1234")

	t.Run("valid token", func(t *testing.T) {
		vr, err := f.h.ValidateToken(context.Background(), &identityv1.ValidateTokenRequest{Token: resp.AccessToken})
		require.NoError(t, err)
		assert.True(t, vr.Valid)
		assert.Equal(t, "carol@example.com", vr.Email)
	})

	t.Run("invalid token", func(t *testing.T) {
		vr, err := f.h.ValidateToken(context.Background(), &identityv1.ValidateTokenRequest{Token: "garbage"})
		require.NoError(t, err)
		assert.False(t, vr.Valid)
	})
}

func TestRefreshToken(t *testing.T) {
	f := newTestFixture()
	register(t, f, "dave", "dave@example.com", "pass1234")
	resp := login(t, f, "dave@example.com", "pass1234")

	t.Run("success", func(t *testing.T) {
		rr, err := f.h.RefreshToken(context.Background(), &identityv1.RefreshTokenRequest{RefreshToken: resp.RefreshToken})
		require.NoError(t, err)
		assert.NotEmpty(t, rr.AccessToken)
	})

	t.Run("invalid refresh token", func(t *testing.T) {
		_, err := f.h.RefreshToken(context.Background(), &identityv1.RefreshTokenRequest{RefreshToken: "bad"})
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})
}

// seedApprovedDevice inserts a fake user + approved device and returns their IDs.
// Each call creates a fresh user with a unique email so it can be used multiple times per test.
func seedApprovedDevice(t *testing.T, f *testFixture) (userID, deviceID string) {
	t.Helper()
	suffix := strconv.Itoa(f.q.nextID + 1)
	user, err := f.q.CreateUser(context.Background(), repository.CreateUserParams{
		Username: "kex-user-" + suffix, Email: "kex" + suffix + "@example.com", PasswordHash: "x",
	})
	require.NoError(t, err)
	dev, err := f.q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		UserID:      user.ID,
		Name:        "laptop",
		DeviceType:  "pc",
		Fingerprint: "fp-" + suffix,
		IsApproved:  true,
		ApprovedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	return user.ID, dev.ID
}

func TestUploadDeviceKey(t *testing.T) {
	f := newTestFixture()
	userID, deviceID := seedApprovedDevice(t, f)
	ctx := authCtx(userID)

	pubKey := make([]byte, 32)
	for i := range pubKey {
		pubKey[i] = byte(i)
	}

	t.Run("success", func(t *testing.T) {
		resp, err := f.h.UploadDeviceKey(ctx, &identityv1.UploadDeviceKeyRequest{
			DeviceId:     deviceID,
			KexAlgo:      "x25519",
			KexPublicKey: pubKey,
		})
		require.NoError(t, err)
		require.NotNil(t, resp.Key)
		assert.Equal(t, deviceID, resp.Key.DeviceId)
		assert.Equal(t, "x25519", resp.Key.KexAlgo)
		assert.Equal(t, pubKey, resp.Key.KexPublicKey)
	})

	t.Run("default algo", func(t *testing.T) {
		resp, err := f.h.UploadDeviceKey(ctx, &identityv1.UploadDeviceKeyRequest{
			DeviceId:     deviceID,
			KexPublicKey: pubKey,
		})
		require.NoError(t, err)
		assert.Equal(t, "x25519", resp.Key.KexAlgo)
	})

	t.Run("rejects wrong key length", func(t *testing.T) {
		_, err := f.h.UploadDeviceKey(ctx, &identityv1.UploadDeviceKeyRequest{
			DeviceId:     deviceID,
			KexPublicKey: []byte{1, 2, 3},
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("missing fields", func(t *testing.T) {
		_, err := f.h.UploadDeviceKey(ctx, &identityv1.UploadDeviceKeyRequest{DeviceId: deviceID})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("foreign device rejected", func(t *testing.T) {
		_, otherDeviceID := seedApprovedDevice(t, f)
		// other device belongs to a different user; current ctx is for first user.
		// seedApprovedDevice creates a new user each time, so otherDeviceID is foreign.
		_, err := f.h.UploadDeviceKey(ctx, &identityv1.UploadDeviceKeyRequest{
			DeviceId:     otherDeviceID,
			KexPublicKey: pubKey,
		})
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})

	t.Run("unauthenticated", func(t *testing.T) {
		_, err := f.h.UploadDeviceKey(context.Background(), &identityv1.UploadDeviceKeyRequest{
			DeviceId:     deviceID,
			KexPublicKey: pubKey,
		})
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})
}

func TestGetDeviceKey(t *testing.T) {
	f := newTestFixture()
	userID, deviceID := seedApprovedDevice(t, f)
	ctx := authCtx(userID)

	pubKey := make([]byte, 32)
	pubKey[0] = 0xAB
	_, err := f.h.UploadDeviceKey(ctx, &identityv1.UploadDeviceKeyRequest{
		DeviceId:     deviceID,
		KexPublicKey: pubKey,
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		resp, err := f.h.GetDeviceKey(ctx, &identityv1.GetDeviceKeyRequest{DeviceId: deviceID})
		require.NoError(t, err)
		require.NotNil(t, resp.Key)
		assert.Equal(t, pubKey, resp.Key.KexPublicKey)
	})

	t.Run("not found when no key uploaded", func(t *testing.T) {
		_, otherDeviceID := seedApprovedDevice(t, f)
		// Use the other user's context for ownership.
		otherCtx := authCtx(f.q.devices[otherDeviceID].UserID)
		_, err := f.h.GetDeviceKey(otherCtx, &identityv1.GetDeviceKeyRequest{DeviceId: otherDeviceID})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestGetSessionDeviceKeys(t *testing.T) {
	f := newTestFixture()
	userID, deviceID := seedApprovedDevice(t, f)
	ctx := authCtx(userID)

	// Create a peer session and add the device.
	sess, err := f.h.CreatePeerSession(ctx, &identityv1.CreatePeerSessionRequest{
		Name: "test-session", DeviceId: deviceID,
	})
	require.NoError(t, err)
	sessionID := sess.Session.SessionId

	pubKey := make([]byte, 32)
	pubKey[0] = 0x42
	_, err = f.h.UploadDeviceKey(ctx, &identityv1.UploadDeviceKeyRequest{
		DeviceId:     deviceID,
		KexPublicKey: pubKey,
	})
	require.NoError(t, err)

	t.Run("returns keys for session devices", func(t *testing.T) {
		resp, err := f.h.GetSessionDeviceKeys(ctx, &identityv1.GetSessionDeviceKeysRequest{SessionId: sessionID})
		require.NoError(t, err)
		require.Len(t, resp.Keys, 1)
		assert.Equal(t, deviceID, resp.Keys[0].DeviceId)
		assert.Equal(t, pubKey, resp.Keys[0].KexPublicKey)
	})

	t.Run("foreign session rejected", func(t *testing.T) {
		otherUserID, _ := seedApprovedDevice(t, f)
		otherCtx := authCtx(otherUserID)
		_, err := f.h.GetSessionDeviceKeys(otherCtx, &identityv1.GetSessionDeviceKeysRequest{SessionId: sessionID})
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})
}
