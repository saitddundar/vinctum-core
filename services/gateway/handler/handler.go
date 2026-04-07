package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	identityv1 "github.com/saitddundar/vinctum-core/proto/identity/v1"
	routingv1 "github.com/saitddundar/vinctum-core/proto/routing/v1"
	transferv1 "github.com/saitddundar/vinctum-core/proto/transfer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type ServiceAddresses struct {
	Identity  string
	Discovery string
	Routing   string
	Transfer  string
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

	// routing proxy
	mux.HandleFunc("POST /api/v1/routes/find", h.handleFindRoute)
	mux.HandleFunc("GET /api/v1/routes/table/{nodeId}", h.handleGetRouteTable)
	mux.HandleFunc("GET /api/v1/relays", h.handleListRelays)

	// transfer proxy
	mux.HandleFunc("POST /api/v1/transfers", h.handleInitiateTransfer)
	mux.HandleFunc("GET /api/v1/transfers/{transferId}", h.handleGetTransferStatus)
	mux.HandleFunc("GET /api/v1/transfers/node/{nodeId}", h.handleListTransfers)
	mux.HandleFunc("POST /api/v1/transfers/{transferId}/cancel", h.handleCancelTransfer)
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
	_ = json.NewDecoder(r.Body).Decode(&body)

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

// ─── Helpers ────────────────────────────────────────────────

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
	return json.NewDecoder(r.Body).Decode(v)
}
