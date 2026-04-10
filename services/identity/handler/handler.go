package handler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/internal/auth"
	"github.com/saitddundar/vinctum-core/pkg/crypto"
	"github.com/saitddundar/vinctum-core/pkg/mailer"
	"github.com/saitddundar/vinctum-core/pkg/middleware"
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
	pairing    *auth.PairingStore
	mailer     *mailer.Mailer
	bcryptCost int
}

func NewIdentityHandler(q repository.Querier, jwt *auth.Manager, bl *auth.TokenBlacklist, ps *auth.PairingStore, m *mailer.Mailer, bcryptCost int) *IdentityHandler {
	return &IdentityHandler{queries: q, jwt: jwt, blacklist: bl, pairing: ps, mailer: m, bcryptCost: bcryptCost}
}

// ─── Auth RPCs (unchanged) ─────────────────────────

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

// ─── Device Management RPCs ────────────────────────

func (h *IdentityHandler) RegisterDevice(ctx context.Context, req *identityv1.RegisterDeviceRequest) (*identityv1.RegisterDeviceResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if req.Name == "" || req.Fingerprint == "" {
		return nil, status.Error(codes.InvalidArgument, "name and fingerprint are required")
	}

	// Check if device already exists with this fingerprint
	existing, err := h.queries.GetDeviceByFingerprint(ctx, repository.GetDeviceByFingerprintParams{
		Column1:     userID,
		Fingerprint: req.Fingerprint,
	})
	if err == nil {
		// Device already registered, update activity and return it
		h.queries.UpdateDeviceActivity(ctx, repository.UpdateDeviceActivityParams{
			Column1: existing.ID,
			NodeID:  pgtype.Text{String: req.NodeId, Valid: req.NodeId != ""},
		})
		return &identityv1.RegisterDeviceResponse{Device: deviceToProto(existing)}, nil
	}

	now := time.Now()
	device, err := h.queries.CreateDevice(ctx, repository.CreateDeviceParams{
		UserID:      userID,
		Name:        req.Name,
		DeviceType:  deviceTypeToString(req.DeviceType),
		NodeID:      pgtype.Text{String: req.NodeId, Valid: req.NodeId != ""},
		Fingerprint: req.Fingerprint,
		IsApproved:  true, // Self-registered via login = auto-approved
		ApprovedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		ApprovedBy:  pgtype.UUID{}, // NULL = self-approved
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to register device")
	}

	log.Info().Str("user_id", userID).Str("device_id", device.ID).Str("name", req.Name).Msg("device registered")

	return &identityv1.RegisterDeviceResponse{Device: deviceToProto(device)}, nil
}

func (h *IdentityHandler) ListDevices(ctx context.Context, _ *identityv1.ListDevicesRequest) (*identityv1.ListDevicesResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	devices, err := h.queries.ListDevicesByUser(ctx, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list devices")
	}

	pbDevices := make([]*identityv1.Device, len(devices))
	for i, d := range devices {
		pbDevices[i] = deviceToProto(d)
	}

	return &identityv1.ListDevicesResponse{Devices: pbDevices}, nil
}

func (h *IdentityHandler) GetDevice(ctx context.Context, req *identityv1.GetDeviceRequest) (*identityv1.GetDeviceResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	device, err := h.queries.GetDeviceByID(ctx, req.DeviceId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "device not found")
		}
		return nil, status.Error(codes.Internal, "failed to get device")
	}

	if device.UserID != userID {
		return nil, status.Error(codes.PermissionDenied, "device does not belong to you")
	}

	return &identityv1.GetDeviceResponse{Device: deviceToProto(device)}, nil
}

func (h *IdentityHandler) RevokeDevice(ctx context.Context, req *identityv1.RevokeDeviceRequest) (*identityv1.RevokeDeviceResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if err := h.queries.RevokeDevice(ctx, repository.RevokeDeviceParams{
		Column1: req.DeviceId,
		Column2: userID,
	}); err != nil {
		return nil, status.Error(codes.Internal, "failed to revoke device")
	}

	log.Info().Str("user_id", userID).Str("device_id", req.DeviceId).Msg("device revoked")

	return &identityv1.RevokeDeviceResponse{Success: true}, nil
}

func (h *IdentityHandler) UpdateDeviceActivity(ctx context.Context, req *identityv1.UpdateDeviceActivityRequest) (*identityv1.UpdateDeviceActivityResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	device, err := h.queries.GetDeviceByID(ctx, req.DeviceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "device not found")
	}
	if device.UserID != userID {
		return nil, status.Error(codes.PermissionDenied, "device does not belong to you")
	}

	if err := h.queries.UpdateDeviceActivity(ctx, repository.UpdateDeviceActivityParams{
		Column1: req.DeviceId,
		NodeID:  pgtype.Text{String: req.NodeId, Valid: req.NodeId != ""},
	}); err != nil {
		return nil, status.Error(codes.Internal, "failed to update device activity")
	}

	return &identityv1.UpdateDeviceActivityResponse{Success: true}, nil
}

func (h *IdentityHandler) GeneratePairingCode(ctx context.Context, req *identityv1.GeneratePairingCodeRequest) (*identityv1.GeneratePairingCodeResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	// Verify the device belongs to the user and is approved
	device, err := h.queries.GetDeviceByID(ctx, req.DeviceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "device not found")
	}
	if device.UserID != userID || !device.IsApproved {
		return nil, status.Error(codes.PermissionDenied, "device not approved")
	}

	code, err := h.pairing.GenerateCode(ctx, userID, req.DeviceId)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate pairing code")
	}

	log.Info().Str("user_id", userID).Str("device_id", req.DeviceId).Msg("pairing code generated")

	return &identityv1.GeneratePairingCodeResponse{
		PairingCode: code,
		ExpiresInS:  300,
	}, nil
}

func (h *IdentityHandler) RedeemPairingCode(ctx context.Context, req *identityv1.RedeemPairingCodeRequest) (*identityv1.RedeemPairingCodeResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if req.PairingCode == "" || req.Name == "" || req.Fingerprint == "" {
		return nil, status.Error(codes.InvalidArgument, "pairing_code, name and fingerprint are required")
	}

	data, err := h.pairing.RedeemCode(ctx, req.PairingCode)
	if err != nil {
		return nil, status.Error(codes.NotFound, "invalid or expired pairing code")
	}

	// Verify same user account
	if data.UserID != userID {
		return nil, status.Error(codes.PermissionDenied, "pairing code belongs to a different account")
	}

	// Create the pending device
	device, err := h.queries.CreateDevice(ctx, repository.CreateDeviceParams{
		UserID:      userID,
		Name:        req.Name,
		DeviceType:  deviceTypeToString(req.DeviceType),
		NodeID:      pgtype.Text{String: req.NodeId, Valid: req.NodeId != ""},
		Fingerprint: req.Fingerprint,
		IsApproved:  false, // Pending approval
		ApprovedAt:  pgtype.Timestamptz{},
		ApprovedBy:  pgtype.UUID{},
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create device")
	}

	log.Info().Str("user_id", userID).Str("device_id", device.ID).Msg("pairing code redeemed, pending approval")

	return &identityv1.RedeemPairingCodeResponse{
		DeviceId:       device.ID,
		ApproverDevice: data.DeviceID,
	}, nil
}

func (h *IdentityHandler) ApprovePairing(ctx context.Context, req *identityv1.ApprovePairingRequest) (*identityv1.ApprovePairingResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	// Verify the approver device belongs to user
	approver, err := h.queries.GetDeviceByID(ctx, req.ApproverDeviceId)
	if err != nil || approver.UserID != userID || !approver.IsApproved {
		return nil, status.Error(codes.PermissionDenied, "approver device not valid")
	}

	// Verify the pending device belongs to same user
	pending, err := h.queries.GetDeviceByID(ctx, req.PendingDeviceId)
	if err != nil || pending.UserID != userID {
		return nil, status.Error(codes.NotFound, "pending device not found")
	}

	if req.Approve {
		if err := h.queries.ApproveDevice(ctx, repository.ApproveDeviceParams{
			Column1: req.PendingDeviceId,
			Column2: req.ApproverDeviceId,
		}); err != nil {
			return nil, status.Error(codes.Internal, "failed to approve device")
		}
		log.Info().Str("device_id", req.PendingDeviceId).Str("approved_by", req.ApproverDeviceId).Msg("device approved")
	} else {
		if err := h.queries.RejectDevice(ctx, req.PendingDeviceId); err != nil {
			return nil, status.Error(codes.Internal, "failed to reject device")
		}
		log.Info().Str("device_id", req.PendingDeviceId).Msg("device rejected")
	}

	// Re-fetch to get updated state
	updated, err := h.queries.GetDeviceByID(ctx, req.PendingDeviceId)
	if err != nil {
		// Device might have been rejected (revoked), return success anyway
		return &identityv1.ApprovePairingResponse{Success: true}, nil
	}

	return &identityv1.ApprovePairingResponse{
		Success: true,
		Device:  deviceToProto(updated),
	}, nil
}

// Peer Session RPCs

func (h *IdentityHandler) CreatePeerSession(ctx context.Context, req *identityv1.CreatePeerSessionRequest) (*identityv1.CreatePeerSessionResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	name := req.Name
	if name == "" {
		name = "Default Session"
	}

	session, err := h.queries.CreatePeerSession(ctx, repository.CreatePeerSessionParams{
		UserID: userID,
		Name:   name,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create session")
	}

	// Auto-join the creating device
	if req.DeviceId != "" {
		h.queries.AddDeviceToSession(ctx, repository.AddDeviceToSessionParams{
			SessionID: session.ID,
			DeviceID:  req.DeviceId,
		})
	}

	devices, _ := h.queries.ListSessionDevices(ctx, session.ID)

	log.Info().Str("user_id", userID).Str("session_id", session.ID).Msg("peer session created")

	return &identityv1.CreatePeerSessionResponse{
		Session: sessionToProto(session, devices),
	}, nil
}

func (h *IdentityHandler) ListPeerSessions(ctx context.Context, _ *identityv1.ListPeerSessionsRequest) (*identityv1.ListPeerSessionsResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	sessions, err := h.queries.ListActivePeerSessions(ctx, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list sessions")
	}

	pbSessions := make([]*identityv1.PeerSession, len(sessions))
	for i, s := range sessions {
		devices, _ := h.queries.ListSessionDevices(ctx, s.ID)
		pbSessions[i] = sessionToProto(s, devices)
	}

	return &identityv1.ListPeerSessionsResponse{Sessions: pbSessions}, nil
}

func (h *IdentityHandler) ClosePeerSession(ctx context.Context, req *identityv1.ClosePeerSessionRequest) (*identityv1.ClosePeerSessionResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if err := h.queries.ClosePeerSession(ctx, repository.ClosePeerSessionParams{
		Column1: req.SessionId,
		Column2: userID,
	}); err != nil {
		return nil, status.Error(codes.Internal, "failed to close session")
	}

	return &identityv1.ClosePeerSessionResponse{Success: true}, nil
}

func (h *IdentityHandler) JoinPeerSession(ctx context.Context, req *identityv1.JoinPeerSessionRequest) (*identityv1.JoinPeerSessionResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	session, err := h.queries.GetPeerSession(ctx, req.SessionId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	if session.UserID != userID {
		return nil, status.Error(codes.PermissionDenied, "session does not belong to you")
	}
	if !session.IsActive {
		return nil, status.Error(codes.FailedPrecondition, "session is closed")
	}

	device, err := h.queries.GetDeviceByID(ctx, req.DeviceId)
	if err != nil || device.UserID != userID {
		return nil, status.Error(codes.NotFound, "device not found")
	}

	if err := h.queries.AddDeviceToSession(ctx, repository.AddDeviceToSessionParams{
		SessionID: req.SessionId,
		DeviceID:  req.DeviceId,
	}); err != nil {
		return nil, status.Error(codes.Internal, "failed to join session")
	}

	return &identityv1.JoinPeerSessionResponse{Success: true}, nil
}

func (h *IdentityHandler) ListSessionDevices(ctx context.Context, req *identityv1.ListSessionDevicesRequest) (*identityv1.ListSessionDevicesResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	session, err := h.queries.GetPeerSession(ctx, req.SessionId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	if session.UserID != userID {
		return nil, status.Error(codes.PermissionDenied, "session does not belong to you")
	}

	devices, err := h.queries.ListSessionDevices(ctx, req.SessionId)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list session devices")
	}

	pbDevices := make([]*identityv1.Device, len(devices))
	for i, d := range devices {
		pbDevices[i] = deviceToProto(d)
	}

	return &identityv1.ListSessionDevicesResponse{Devices: pbDevices}, nil
}

func (h *IdentityHandler) LeavePeerSession(ctx context.Context, req *identityv1.LeavePeerSessionRequest) (*identityv1.LeavePeerSessionResponse, error) {
	userID, ok := middleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	session, err := h.queries.GetPeerSession(ctx, req.SessionId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	if session.UserID != userID {
		return nil, status.Error(codes.PermissionDenied, "session does not belong to you")
	}

	if err := h.queries.RemoveDeviceFromSession(ctx, repository.RemoveDeviceFromSessionParams{
		Column1: req.SessionId,
		Column2: req.DeviceId,
	}); err != nil {
		return nil, status.Error(codes.Internal, "failed to leave session")
	}

	return &identityv1.LeavePeerSessionResponse{Success: true}, nil
}

// ─── Helpers ────────────────────────────────────────

func deviceToProto(d repository.Device) *identityv1.Device {
	pb := &identityv1.Device{
		DeviceId:    d.ID,
		UserId:      d.UserID,
		Name:        d.Name,
		DeviceType:  stringToDeviceType(d.DeviceType),
		Fingerprint: d.Fingerprint,
		IsApproved:  d.IsApproved,
		LastActive:  timestamppb.New(d.LastActive),
		CreatedAt:   timestamppb.New(d.CreatedAt),
		IsRevoked:   d.RevokedAt.Valid,
	}
	if d.NodeID.Valid {
		pb.NodeId = d.NodeID.String
	}
	if d.ApprovedAt.Valid {
		pb.ApprovedAt = timestamppb.New(d.ApprovedAt.Time)
	}
	if d.ApprovedBy.Valid {
		bytes := d.ApprovedBy.Bytes
		pb.ApprovedByDeviceId = fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
	}
	return pb
}

func sessionToProto(s repository.PeerSession, devices []repository.Device) *identityv1.PeerSession {
	pb := &identityv1.PeerSession{
		SessionId: s.ID,
		UserId:    s.UserID,
		Name:      s.Name,
		IsActive:  s.IsActive,
		CreatedAt: timestamppb.New(s.CreatedAt),
	}
	if s.ClosedAt.Valid {
		pb.ClosedAt = timestamppb.New(s.ClosedAt.Time)
	}
	for _, d := range devices {
		pb.Devices = append(pb.Devices, deviceToProto(d))
	}
	return pb
}

func deviceTypeToString(dt identityv1.DeviceType) string {
	switch dt {
	case identityv1.DeviceType_DEVICE_TYPE_PHONE:
		return "phone"
	case identityv1.DeviceType_DEVICE_TYPE_TABLET:
		return "tablet"
	default:
		return "pc"
	}
}

func stringToDeviceType(s string) identityv1.DeviceType {
	switch s {
	case "phone":
		return identityv1.DeviceType_DEVICE_TYPE_PHONE
	case "tablet":
		return identityv1.DeviceType_DEVICE_TYPE_TABLET
	default:
		return identityv1.DeviceType_DEVICE_TYPE_PC
	}
}
