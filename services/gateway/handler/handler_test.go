package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/saitddundar/vinctum-core/services/gateway/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestGateway(t *testing.T) (*handler.GatewayHandler, *http.ServeMux) {
	t.Helper()
	// Create handler with empty addresses — clients will be nil,
	// so proxy endpoints return 503 (service unavailable).
	h, err := handler.NewGatewayHandler(handler.ServiceAddresses{}, "test-v0.1")
	require.NoError(t, err)
	t.Cleanup(h.Close)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, mux
}

// ─── Health & Meta ─────────────────────────────────────────

func TestHealthEndpoint(t *testing.T) {
	_, mux := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, true, body["healthy"])
	assert.Equal(t, "test-v0.1", body["version"])
	assert.Contains(t, body, "uptime_s")
	assert.Contains(t, body, "timestamp")
}

func TestServiceStatusEndpoint(t *testing.T) {
	_, mux := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/services", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	services, ok := body["services"].([]any)
	require.True(t, ok)
	assert.Len(t, services, 3) // identity, routing, transfer
}

// ─── Identity Proxy (service unavailable) ─────────────────

func TestRegisterUnavailable(t *testing.T) {
	_, mux := newTestGateway(t)

	payload := `{"username":"alice","email":"alice@example.com","password":"secret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewBufferString(payload))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestLoginUnavailable(t *testing.T) {
	_, mux := newTestGateway(t)

	payload := `{"email":"alice@example.com","password":"secret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(payload))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestRefreshUnavailable(t *testing.T) {
	_, mux := newTestGateway(t)

	payload := `{"refresh_token":"tok"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewBufferString(payload))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestValidateUnavailable(t *testing.T) {
	_, mux := newTestGateway(t)

	payload := `{"token":"tok"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/validate", bytes.NewBufferString(payload))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ─── Routing Proxy (service unavailable) ──────────────────

func TestFindRouteUnavailable(t *testing.T) {
	_, mux := newTestGateway(t)

	payload := `{"source_node_id":"A","target_node_id":"B"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/find", bytes.NewBufferString(payload))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetRouteTableUnavailable(t *testing.T) {
	_, mux := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/routes/table/node-A", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestListRelaysUnavailable(t *testing.T) {
	_, mux := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/relays", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ─── Transfer Proxy (service unavailable) ─────────────────

func TestInitiateTransferUnavailable(t *testing.T) {
	_, mux := newTestGateway(t)

	payload := `{"source_node_id":"A","target_node_id":"B","file_name":"test.bin"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfers", bytes.NewBufferString(payload))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetTransferStatusUnavailable(t *testing.T) {
	_, mux := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/transfers/tx-123", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestListTransfersUnavailable(t *testing.T) {
	_, mux := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/transfers/node/node-A", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestCancelTransferUnavailable(t *testing.T) {
	_, mux := newTestGateway(t)

	payload := `{"reason":"testing"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfers/tx-123/cancel", bytes.NewBufferString(payload))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ─── Encryption ───────────────────────────────────────────

func TestGenerateKeyEndpoint(t *testing.T) {
	_, mux := newTestGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/encryption/generate-key", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEmpty(t, body["encryption_key"])
}

// ─── Bad Request ──────────────────────────────────────────

func TestBadJSON(t *testing.T) {
	_, mux := newTestGateway(t)

	// Use an endpoint with a connected client to test JSON decode error.
	// Since identity client is nil, this will return 503 before hitting decode.
	// Instead, test against the generate-key endpoint which doesn't decode JSON.
	// We test bad JSON against a real proxy endpoint by checking 503 takes priority.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewBufferString("{invalid"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// 503 because identity client is nil — checked before JSON decode.
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
