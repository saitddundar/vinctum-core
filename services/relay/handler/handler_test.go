package handler_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	relayv1 "github.com/saitddundar/vinctum-core/proto/relay/v1"
	routingv1 "github.com/saitddundar/vinctum-core/proto/routing/v1"
	"github.com/saitddundar/vinctum-core/services/relay/handler"
	"github.com/saitddundar/vinctum-core/services/transfer/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newChunkStore(t *testing.T) storage.ChunkStore {
	t.Helper()
	store, err := storage.NewFileStore(t.TempDir())
	require.NoError(t, err)
	return store
}

func newTestHandler(t *testing.T) *handler.RelayHandler {
	t.Helper()
	return handler.NewRelayHandler("local-node", newChunkStore(t), nil, nil, nil)
}

// ──────────────────── RelayChunk ─────────────────────────

func TestRelayChunk(t *testing.T) {
	ctx := context.Background()

	t.Run("store at final destination (no hops)", func(t *testing.T) {
		h := newTestHandler(t)
		data := []byte("hello chunk")
		hash := sha256.Sum256(data)

		resp, err := h.RelayChunk(ctx, &relayv1.RelayChunkRequest{
			TransferId: "tx-1",
			ChunkIndex: 0,
			Data:       data,
			ChunkHash:  hex.EncodeToString(hash[:]),
			Ttl:        5,
		})
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, "local-node", resp.NodeId)
	})

	t.Run("store when self is final hop", func(t *testing.T) {
		h := newTestHandler(t)
		data := []byte("final hop data")

		resp, err := h.RelayChunk(ctx, &relayv1.RelayChunkRequest{
			TransferId: "tx-2",
			ChunkIndex: 0,
			Data:       data,
			Ttl:        3,
			RemainingHops: []*routingv1.RouteHop{
				{NodeId: "local-node"},
			},
		})
		require.NoError(t, err)
		assert.True(t, resp.Success)
	})

	t.Run("TTL expired", func(t *testing.T) {
		h := newTestHandler(t)
		_, err := h.RelayChunk(ctx, &relayv1.RelayChunkRequest{
			TransferId: "tx-3",
			ChunkIndex: 0,
			Data:       []byte("data"),
			Ttl:        0,
		})
		assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	})

	t.Run("hash mismatch", func(t *testing.T) {
		h := newTestHandler(t)
		_, err := h.RelayChunk(ctx, &relayv1.RelayChunkRequest{
			TransferId: "tx-4",
			ChunkIndex: 0,
			Data:       []byte("data"),
			ChunkHash:  "badhash",
			Ttl:        5,
		})
		assert.Equal(t, codes.DataLoss, status.Code(err))
	})

	t.Run("missing fields", func(t *testing.T) {
		h := newTestHandler(t)
		_, err := h.RelayChunk(ctx, &relayv1.RelayChunkRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("negative TTL", func(t *testing.T) {
		h := newTestHandler(t)
		_, err := h.RelayChunk(ctx, &relayv1.RelayChunkRequest{
			TransferId: "tx-5",
			ChunkIndex: 0,
			Data:       []byte("data"),
			Ttl:        -1,
		})
		assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	})
}

// ──────────────────── FetchChunk ─────────────────────────

func TestFetchChunk(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		h := newTestHandler(t)
		data := []byte("stored chunk data")

		// First store a chunk via RelayChunk.
		_, err := h.RelayChunk(ctx, &relayv1.RelayChunkRequest{
			TransferId: "tx-10",
			ChunkIndex: 0,
			Data:       data,
			Ttl:        5,
		})
		require.NoError(t, err)

		// Now fetch it.
		resp, err := h.FetchChunk(ctx, &relayv1.FetchChunkRequest{
			TransferId: "tx-10",
			ChunkIndex: 0,
		})
		require.NoError(t, err)
		assert.Equal(t, data, resp.Data)
		assert.NotEmpty(t, resp.ChunkHash)
	})

	t.Run("not found", func(t *testing.T) {
		h := newTestHandler(t)
		_, err := h.FetchChunk(ctx, &relayv1.FetchChunkRequest{
			TransferId: "nonexistent",
			ChunkIndex: 0,
		})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("missing transfer_id", func(t *testing.T) {
		h := newTestHandler(t)
		_, err := h.FetchChunk(ctx, &relayv1.FetchChunkRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

// ──────────────────── Ping ───────────────────────────────

func TestPing(t *testing.T) {
	h := newTestHandler(t)
	resp, err := h.Ping(context.Background(), &relayv1.PingRequest{})
	require.NoError(t, err)
	assert.Equal(t, "local-node", resp.NodeId)
	assert.Greater(t, resp.Timestamp, int64(0))
}
