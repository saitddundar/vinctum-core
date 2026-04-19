package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	identityv1 "github.com/saitddundar/vinctum-core/proto/identity/v1"
	routingv1 "github.com/saitddundar/vinctum-core/proto/routing/v1"
	transferv1 "github.com/saitddundar/vinctum-core/proto/transfer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type ServiceAddresses struct {
	Identity  string
	Discovery string
	Routing   string
	Transfer  string
	ML        string
}

type GatewayHandler struct {
	addrs     ServiceAddresses
	version   string
	startedAt time.Time

	identityConn  *grpc.ClientConn
	routingConn   *grpc.ClientConn
	transferConn  *grpc.ClientConn

	identityClient  identityv1.IdentityServiceClient
	routingClient   routingv1.RoutingServiceClient
	transferClient  transferv1.TransferServiceClient

	mlAPIKey string
}

func NewGatewayHandler(addrs ServiceAddresses, version string) (*GatewayHandler, error) {
	h := &GatewayHandler{
		addrs:     addrs,
		version:   version,
		startedAt: time.Now(),
	}

	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	var err error

	if addrs.Identity != "" {
		h.identityConn, err = grpc.NewClient(addrs.Identity, dialOpts...)
		if err != nil {
			return nil, fmt.Errorf("dial identity: %w", err)
		}
		h.identityClient = identityv1.NewIdentityServiceClient(h.identityConn)
	}

	if addrs.Routing != "" {
		h.routingConn, err = grpc.NewClient(addrs.Routing, dialOpts...)
		if err != nil {
			return nil, fmt.Errorf("dial routing: %w", err)
		}
		h.routingClient = routingv1.NewRoutingServiceClient(h.routingConn)
	}

	if addrs.Transfer != "" {
		h.transferConn, err = grpc.NewClient(addrs.Transfer, dialOpts...)
		if err != nil {
			return nil, fmt.Errorf("dial transfer: %w", err)
		}
		h.transferClient = transferv1.NewTransferServiceClient(h.transferConn)
	}

	return h, nil
}

// Close tears down all gRPC connections.
func (h *GatewayHandler) Close() {
	if h.identityConn != nil {
		h.identityConn.Close()
	}
	if h.routingConn != nil {
		h.routingConn.Close()
	}
	if h.transferConn != nil {
		h.transferConn.Close()
	}
}

func (h *GatewayHandler) SetMLAPIKey(key string) {
	h.mlAPIKey = key
}

func (h *GatewayHandler) RegisterRoutes(mux *http.ServeMux) {
	// health & meta
	mux.HandleFunc("GET /health", h.handleHealth)
	mux.HandleFunc("GET /services", h.handleServiceStatus)

	// identity proxy
	mux.HandleFunc("POST /api/v1/auth/register", h.handleRegister)
	mux.HandleFunc("POST /api/v1/auth/login", h.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.handleRefresh)
	mux.HandleFunc("POST /api/v1/auth/validate", h.handleValidate)
	mux.HandleFunc("POST /api/v1/auth/verify", h.handleVerifyEmail)
	mux.HandleFunc("POST /api/v1/auth/resend-verification", h.handleResendVerification)

	// device management
	mux.HandleFunc("POST /api/v1/devices", h.handleRegisterDevice)
	mux.HandleFunc("GET /api/v1/devices", h.handleListDevices)
	mux.HandleFunc("GET /api/v1/devices/{deviceId}", h.handleGetDevice)
	mux.HandleFunc("DELETE /api/v1/devices/{deviceId}", h.handleRevokeDevice)
	mux.HandleFunc("PUT /api/v1/devices/{deviceId}/activity", h.handleUpdateDeviceActivity)

	// pairing
	mux.HandleFunc("POST /api/v1/devices/pairing/generate", h.handleGeneratePairingCode)
	mux.HandleFunc("POST /api/v1/devices/pairing/redeem", h.handleRedeemPairingCode)
	mux.HandleFunc("POST /api/v1/devices/pairing/approve", h.handleApprovePairing)

	// peer sessions
	mux.HandleFunc("POST /api/v1/sessions", h.handleCreatePeerSession)
	mux.HandleFunc("GET /api/v1/sessions", h.handleListPeerSessions)
	mux.HandleFunc("POST /api/v1/sessions/{sessionId}/close", h.handleClosePeerSession)
	mux.HandleFunc("POST /api/v1/sessions/{sessionId}/join", h.handleJoinPeerSession)
	mux.HandleFunc("POST /api/v1/sessions/{sessionId}/leave", h.handleLeavePeerSession)
	mux.HandleFunc("GET /api/v1/sessions/{sessionId}/devices", h.handleListSessionDevices)

	// device keys (E2E key exchange)
	mux.HandleFunc("POST /api/v1/devices/{deviceId}/key", h.handleUploadDeviceKey)
	mux.HandleFunc("GET /api/v1/devices/{deviceId}/key", h.handleGetDeviceKey)
	mux.HandleFunc("GET /api/v1/sessions/{sessionId}/keys", h.handleGetSessionDeviceKeys)

	// routing proxy
	mux.HandleFunc("POST /api/v1/routes/find", h.handleFindRoute)
	mux.HandleFunc("GET /api/v1/routes/table/{nodeId}", h.handleGetRouteTable)
	mux.HandleFunc("GET /api/v1/relays", h.handleListRelays)

	// ml proxy
	mux.HandleFunc("GET /api/v1/ml/health", h.handleMLHealth)
	mux.HandleFunc("POST /api/v1/ml/score", h.handleMLScore)
	mux.HandleFunc("POST /api/v1/ml/anomaly", h.handleMLAnomaly)
	mux.HandleFunc("POST /api/v1/ml/route", h.handleMLRoute)

	// transfer proxy
	mux.HandleFunc("POST /api/v1/transfers", h.handleInitiateTransfer)
	mux.HandleFunc("GET /api/v1/transfers/{transferId}", h.handleGetTransferStatus)
	mux.HandleFunc("GET /api/v1/node-transfers/{nodeId}", h.handleListTransfers)
	mux.HandleFunc("POST /api/v1/transfers/{transferId}/cancel", h.handleCancelTransfer)

	// P2P connection info
	mux.HandleFunc("GET /api/v1/transfers/{transferId}/p2p-info", h.handleGetP2PConnectionInfo)
	mux.HandleFunc("POST /api/v1/transfers/{transferId}/confirm-p2p", h.handleConfirmP2PTransfer)

	// chunk upload/download (bridges HTTP to gRPC streaming)
	mux.HandleFunc("POST /api/v1/chunks/{transferId}", h.handleUploadChunk)
	mux.HandleFunc("GET /api/v1/chunks/{transferId}", h.handleDownloadChunks)

	// transfer watch (long-lived NDJSON stream of transfer events)
	mux.HandleFunc("GET /api/v1/transfer-events", h.handleWatchTransfers)
}

// ─── Health ─────────────────────────────────────────────────

func (h *GatewayHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"healthy":   true,
		"version":   h.version,
		"uptime_s":  int(time.Since(h.startedAt).Seconds()),
		"timestamp": time.Now().UTC(),
	})
}

func (h *GatewayHandler) handleServiceStatus(w http.ResponseWriter, r *http.Request) {
	type svcStatus struct {
		Name      string `json:"name"`
		Healthy   bool   `json:"healthy"`
		Address   string `json:"address"`
		LatencyMs int64  `json:"latency_ms"`
	}

	check := func(name, addr string, conn *grpc.ClientConn) svcStatus {
		s := svcStatus{Name: name, Address: addr}
		if conn == nil {
			return s
		}
		start := time.Now()
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		// A lightweight connectivity check.
		state := conn.GetState()
		_ = ctx
		s.Healthy = state.String() != "SHUTDOWN"
		s.LatencyMs = time.Since(start).Milliseconds()
		return s
	}

	statuses := []svcStatus{
		check("identity", h.addrs.Identity, h.identityConn),
		check("routing", h.addrs.Routing, h.routingConn),
		check("transfer", h.addrs.Transfer, h.transferConn),
	}

	// ML service check (HTTP, not gRPC).
	if h.addrs.ML != "" {
		mlStatus := svcStatus{Name: "ml", Address: h.addrs.ML}
		start := time.Now()
		mlReq, _ := http.NewRequestWithContext(r.Context(), "GET", h.addrs.ML+"/health", nil)
		if mlReq != nil {
			if resp, err := http.DefaultClient.Do(mlReq); err == nil {
				resp.Body.Close()
				mlStatus.Healthy = resp.StatusCode == http.StatusOK
			}
		}
		mlStatus.LatencyMs = time.Since(start).Milliseconds()
		statuses = append(statuses, mlStatus)
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": statuses})
}

// ─── Identity Proxy ─────────────────────────────────────────

func (h *GatewayHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}

	var req identityv1.RegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.identityClient.Register(r.Context(), &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *GatewayHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}

	var req identityv1.LoginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.identityClient.Login(r.Context(), &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}

	var req identityv1.RefreshTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.identityClient.RefreshToken(r.Context(), &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleValidate(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}

	var req identityv1.ValidateTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.identityClient.ValidateToken(r.Context(), &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}

	var req identityv1.VerifyEmailRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.identityClient.VerifyEmail(r.Context(), &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleResendVerification(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}

	var req identityv1.ResendVerificationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.identityClient.ResendVerification(r.Context(), &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── Device Management Proxy ───────────────────────────────

func (h *GatewayHandler) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	var req identityv1.RegisterDeviceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := forwardAuth(r)
	resp, err := h.identityClient.RegisterDevice(ctx, &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *GatewayHandler) handleListDevices(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	ctx := forwardAuth(r)
	resp, err := h.identityClient.ListDevices(ctx, &identityv1.ListDevicesRequest{})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	deviceID := r.PathValue("deviceId")
	ctx := forwardAuth(r)
	resp, err := h.identityClient.GetDevice(ctx, &identityv1.GetDeviceRequest{DeviceId: deviceID})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	deviceID := r.PathValue("deviceId")
	ctx := forwardAuth(r)
	resp, err := h.identityClient.RevokeDevice(ctx, &identityv1.RevokeDeviceRequest{DeviceId: deviceID})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleUpdateDeviceActivity(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	deviceID := r.PathValue("deviceId")
	var body struct {
		NodeID string `json:"node_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := forwardAuth(r)
	resp, err := h.identityClient.UpdateDeviceActivity(ctx, &identityv1.UpdateDeviceActivityRequest{
		DeviceId: deviceID,
		NodeId:   body.NodeID,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── Pairing Proxy ─────────────────────────────────────────

func (h *GatewayHandler) handleGeneratePairingCode(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	var req identityv1.GeneratePairingCodeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := forwardAuth(r)
	resp, err := h.identityClient.GeneratePairingCode(ctx, &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleRedeemPairingCode(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	var req identityv1.RedeemPairingCodeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := forwardAuth(r)
	resp, err := h.identityClient.RedeemPairingCode(ctx, &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleApprovePairing(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	var req identityv1.ApprovePairingRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := forwardAuth(r)
	resp, err := h.identityClient.ApprovePairing(ctx, &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── Peer Session Proxy ────────────────────────────────────

func (h *GatewayHandler) handleCreatePeerSession(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	var req identityv1.CreatePeerSessionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := forwardAuth(r)
	resp, err := h.identityClient.CreatePeerSession(ctx, &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *GatewayHandler) handleListPeerSessions(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	ctx := forwardAuth(r)
	resp, err := h.identityClient.ListPeerSessions(ctx, &identityv1.ListPeerSessionsRequest{})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleClosePeerSession(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	sessionID := r.PathValue("sessionId")
	ctx := forwardAuth(r)
	resp, err := h.identityClient.ClosePeerSession(ctx, &identityv1.ClosePeerSessionRequest{SessionId: sessionID})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleJoinPeerSession(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	sessionID := r.PathValue("sessionId")
	var body struct {
		DeviceID string `json:"device_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := forwardAuth(r)
	resp, err := h.identityClient.JoinPeerSession(ctx, &identityv1.JoinPeerSessionRequest{
		SessionId: sessionID,
		DeviceId:  body.DeviceID,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleListSessionDevices(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	sessionID := r.PathValue("sessionId")
	ctx := forwardAuth(r)
	resp, err := h.identityClient.ListSessionDevices(ctx, &identityv1.ListSessionDevicesRequest{
		SessionId: sessionID,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleLeavePeerSession(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	sessionID := r.PathValue("sessionId")
	var body struct {
		DeviceID string `json:"device_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := forwardAuth(r)
	resp, err := h.identityClient.LeavePeerSession(ctx, &identityv1.LeavePeerSessionRequest{
		SessionId: sessionID,
		DeviceId:  body.DeviceID,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── Device Keys ────────────────────────────────────────────

func (h *GatewayHandler) handleUploadDeviceKey(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	deviceID := r.PathValue("deviceId")
	var body struct {
		KexAlgo      string `json:"kex_algo"`
		KexPublicKey []byte `json:"kex_public_key"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := forwardAuth(r)
	resp, err := h.identityClient.UploadDeviceKey(ctx, &identityv1.UploadDeviceKeyRequest{
		DeviceId:     deviceID,
		KexAlgo:      body.KexAlgo,
		KexPublicKey: body.KexPublicKey,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleGetDeviceKey(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	deviceID := r.PathValue("deviceId")
	ctx := forwardAuth(r)
	resp, err := h.identityClient.GetDeviceKey(ctx, &identityv1.GetDeviceKeyRequest{
		DeviceId: deviceID,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleGetSessionDeviceKeys(w http.ResponseWriter, r *http.Request) {
	if h.identityClient == nil {
		writeError(w, http.StatusServiceUnavailable, "identity service unavailable")
		return
	}
	sessionID := r.PathValue("sessionId")
	ctx := forwardAuth(r)
	resp, err := h.identityClient.GetSessionDeviceKeys(ctx, &identityv1.GetSessionDeviceKeysRequest{
		SessionId: sessionID,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── Routing Proxy ──────────────────────────────────────────

func (h *GatewayHandler) handleFindRoute(w http.ResponseWriter, r *http.Request) {
	if h.routingClient == nil {
		writeError(w, http.StatusServiceUnavailable, "routing service unavailable")
		return
	}

	var req routingv1.FindRouteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := forwardAuth(r)
	resp, err := h.routingClient.FindRoute(ctx, &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleGetRouteTable(w http.ResponseWriter, r *http.Request) {
	if h.routingClient == nil {
		writeError(w, http.StatusServiceUnavailable, "routing service unavailable")
		return
	}

	nodeID := r.PathValue("nodeId")
	ctx := forwardAuth(r)

	resp, err := h.routingClient.GetRouteTable(ctx, &routingv1.GetRouteTableRequest{NodeId: nodeID})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleListRelays(w http.ResponseWriter, r *http.Request) {
	if h.routingClient == nil {
		writeError(w, http.StatusServiceUnavailable, "routing service unavailable")
		return
	}

	ctx := forwardAuth(r)
	resp, err := h.routingClient.ListRelays(ctx, &routingv1.ListRelaysRequest{Limit: 50})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── Transfer Proxy ─────────────────────────────────────────

func (h *GatewayHandler) handleInitiateTransfer(w http.ResponseWriter, r *http.Request) {
	if h.transferClient == nil {
		writeError(w, http.StatusServiceUnavailable, "transfer service unavailable")
		return
	}

	var req transferv1.InitiateTransferRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := forwardAuth(r)

	// Verify sender node belongs to authenticated user.
	if req.SenderNodeId != "" {
		if !h.userOwnsNode(ctx, req.SenderNodeId) {
			writeError(w, http.StatusForbidden, "sender node does not belong to you")
			return
		}
	}

	resp, err := h.transferClient.InitiateTransfer(ctx, &req)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *GatewayHandler) handleGetTransferStatus(w http.ResponseWriter, r *http.Request) {
	if h.transferClient == nil {
		writeError(w, http.StatusServiceUnavailable, "transfer service unavailable")
		return
	}

	transferID := r.PathValue("transferId")
	ctx := forwardAuth(r)

	resp, err := h.transferClient.GetTransferStatus(ctx, &transferv1.GetTransferStatusRequest{TransferId: transferID})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleListTransfers(w http.ResponseWriter, r *http.Request) {
	if h.transferClient == nil {
		writeError(w, http.StatusServiceUnavailable, "transfer service unavailable")
		return
	}

	nodeID := r.PathValue("nodeId")
	ctx := forwardAuth(r)

	// Verify requested node belongs to authenticated user.
	if !h.userOwnsNode(ctx, nodeID) {
		writeError(w, http.StatusForbidden, "node does not belong to you")
		return
	}

	resp, err := h.transferClient.ListTransfers(ctx, &transferv1.ListTransfersRequest{NodeId: nodeID})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleCancelTransfer(w http.ResponseWriter, r *http.Request) {
	if h.transferClient == nil {
		writeError(w, http.StatusServiceUnavailable, "transfer service unavailable")
		return
	}

	transferID := r.PathValue("transferId")
	ctx := forwardAuth(r)

	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.transferClient.CancelTransfer(ctx, &transferv1.CancelTransferRequest{
		TransferId: transferID,
		Reason:     body.Reason,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── P2P Connection Info ────────────────────────────────────

func (h *GatewayHandler) handleGetP2PConnectionInfo(w http.ResponseWriter, r *http.Request) {
	if h.transferClient == nil {
		writeError(w, http.StatusServiceUnavailable, "transfer service unavailable")
		return
	}

	transferID := r.PathValue("transferId")
	ctx := forwardAuth(r)

	resp, err := h.transferClient.GetP2PConnectionInfo(ctx, &transferv1.GetP2PConnectionInfoRequest{TransferId: transferID})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleConfirmP2PTransfer(w http.ResponseWriter, r *http.Request) {
	if h.transferClient == nil {
		writeError(w, http.StatusServiceUnavailable, "transfer service unavailable")
		return
	}

	transferID := r.PathValue("transferId")
	var body struct {
		ConfirmingNodeID  string `json:"confirming_node_id"`
		ChunksTransferred int32  `json:"chunks_transferred"`
		Success           bool   `json:"success"`
		ErrorMessage      string `json:"error_message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := forwardAuth(r)
	resp, err := h.transferClient.ConfirmP2PTransfer(ctx, &transferv1.ConfirmP2PTransferRequest{
		TransferId:        transferID,
		ConfirmingNodeId:  body.ConfirmingNodeID,
		ChunksTransferred: body.ChunksTransferred,
		Success:           body.Success,
		ErrorMessage:      body.ErrorMessage,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── Chunk Upload/Download (HTTP ↔ gRPC Streaming Bridge) ──

func (h *GatewayHandler) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	if h.transferClient == nil {
		writeError(w, http.StatusServiceUnavailable, "transfer service unavailable")
		return
	}

	transferID := r.PathValue("transferId")
	ctx := forwardAuth(r)

	// Parse multipart form (max 10MB per chunk + overhead)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	chunkIndexStr := r.FormValue("chunk_index")
	chunkHash := r.FormValue("chunk_hash")

	var chunkIndex int
	if _, err := fmt.Sscanf(chunkIndexStr, "%d", &chunkIndex); err != nil {
		writeError(w, http.StatusBadRequest, "invalid chunk_index")
		return
	}

	file, _, err := r.FormFile("data")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing data file in multipart form")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, 10<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read chunk data")
		return
	}

	// Open gRPC stream and send single chunk
	stream, err := h.transferClient.SendChunk(ctx)
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	if err := stream.Send(&transferv1.SendChunkRequest{
		TransferId: transferID,
		ChunkIndex: int32(chunkIndex),
		Data:       data,
		ChunkHash:  chunkHash,
	}); err != nil {
		writeGRPCError(w, err)
		return
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *GatewayHandler) handleDownloadChunks(w http.ResponseWriter, r *http.Request) {
	if h.transferClient == nil {
		writeError(w, http.StatusServiceUnavailable, "transfer service unavailable")
		return
	}

	transferID := r.PathValue("transferId")
	ctx := forwardAuth(r)

	startChunk := int32(0)
	if sc := r.URL.Query().Get("start_chunk"); sc != "" {
		var n int
		if _, err := fmt.Sscanf(sc, "%d", &n); err == nil {
			startChunk = int32(n)
		}
	}

	receiverNodeID := r.URL.Query().Get("receiver_node_id")

	stream, err := h.transferClient.ReceiveChunks(ctx, &transferv1.ReceiveChunksRequest{
		TransferId:     transferID,
		StartChunk:     startChunk,
		ReceiverNodeId: receiverNodeID,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	// Stream chunks as NDJSON (newline-delimited JSON)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	encoder := json.NewEncoder(w)

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Write error as final NDJSON line
			encoder.Encode(map[string]string{"error": err.Error()})
			break
		}

		chunkResp := map[string]any{
			"transfer_id": chunk.TransferId,
			"chunk_index": chunk.ChunkIndex,
			"data":        chunk.Data, // base64 encoded by json.Marshal
			"chunk_hash":  chunk.ChunkHash,
			"is_last":     chunk.IsLast,
		}
		if err := encoder.Encode(chunkResp); err != nil {
			break
		}
		if canFlush {
			flusher.Flush()
		}
	}
}

func (h *GatewayHandler) handleWatchTransfers(w http.ResponseWriter, r *http.Request) {
	if h.transferClient == nil {
		writeError(w, http.StatusServiceUnavailable, "transfer service unavailable")
		return
	}

	nodeID := r.URL.Query().Get("node_id")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id query parameter is required")
		return
	}

	ctx := forwardAuth(r)

	// Verify the watched node belongs to the authenticated user. Watching
	// someone else's transfers would leak filenames and traffic patterns.
	if !h.userOwnsNode(ctx, nodeID) {
		writeError(w, http.StatusForbidden, "node does not belong to you")
		return
	}

	receiverOnly := r.URL.Query().Get("receiver_only") == "true"

	stream, err := h.transferClient.WatchTransfers(ctx, &transferv1.WatchTransfersRequest{
		NodeId:       nodeID,
		ReceiverOnly: receiverOnly,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	encoder := json.NewEncoder(w)

	for {
		event, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			encoder.Encode(map[string]string{"error": err.Error()})
			if canFlush {
				flusher.Flush()
			}
			return
		}

		if err := encoder.Encode(event); err != nil {
			return
		}
		if canFlush {
			flusher.Flush()
		}
	}
}

// ─── ML Proxy ───────────────────────────────────────────────

func (h *GatewayHandler) proxyML(w http.ResponseWriter, r *http.Request, method, path string) {
	if h.addrs.ML == "" {
		writeError(w, http.StatusServiceUnavailable, "ml service unavailable")
		return
	}

	var bodyReader io.Reader
	if r.Body != nil {
		bodyReader = r.Body
		defer r.Body.Close()
	}

	url := h.addrs.ML + path
	req, err := http.NewRequestWithContext(r.Context(), method, url, bodyReader)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create ml request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if h.mlAPIKey != "" {
		req.Header.Set("X-API-Key", h.mlAPIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Warn().Err(err).Str("path", path).Msg("ml proxy error")
		writeError(w, http.StatusBadGateway, "ml service unreachable")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (h *GatewayHandler) handleMLHealth(w http.ResponseWriter, r *http.Request) {
	h.proxyML(w, r, "GET", "/health")
}

func (h *GatewayHandler) handleMLScore(w http.ResponseWriter, r *http.Request) {
	h.proxyML(w, r, "POST", "/score")
}

func (h *GatewayHandler) handleMLAnomaly(w http.ResponseWriter, r *http.Request) {
	h.proxyML(w, r, "POST", "/anomaly")
}

func (h *GatewayHandler) handleMLRoute(w http.ResponseWriter, r *http.Request) {
	h.proxyML(w, r, "POST", "/route")
}

// ─── Helpers ────────────────────────────────────────────────

// userOwnsNode checks if the authenticated user has a device with the given node_id.
func (h *GatewayHandler) userOwnsNode(ctx context.Context, nodeID string) bool {
	if h.identityClient == nil || nodeID == "" {
		return false
	}
	resp, err := h.identityClient.ListDevices(ctx, &identityv1.ListDevicesRequest{})
	if err != nil {
		return false
	}
	for _, d := range resp.Devices {
		if d.NodeId == nodeID {
			return true
		}
	}
	return false
}

func forwardAuth(r *http.Request) context.Context {
	ctx := r.Context()
	auth := r.Header.Get("Authorization")
	if auth != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", auth)
	}
	return ctx
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if msg, ok := v.(proto.Message); ok {
		b, err := protojson.MarshalOptions{EmitUnpopulated: true, UseProtoNames: true}.Marshal(msg)
		if err != nil {
			log.Error().Err(err).Msg("failed to encode proto response")
			return
		}
		w.Write(b)
		w.Write([]byte("\n"))
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Error().Err(err).Msg("failed to encode response")
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeGRPCError(w http.ResponseWriter, err error) {
	log.Warn().Err(err).Msg("gRPC call failed")
	writeError(w, http.StatusBadGateway, err.Error())
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	if msg, ok := v.(proto.Message); ok {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}
		return protojson.UnmarshalOptions{DiscardUnknown: true, AllowPartial: true}.Unmarshal(body, msg)
	}
	return json.NewDecoder(r.Body).Decode(v)
}
