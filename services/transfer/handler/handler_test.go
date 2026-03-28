package handler_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	transferv1 "github.com/saitddundar/vinctum-core/proto/transfer/v1"
	"github.com/saitddundar/vinctum-core/services/transfer/handler"
	"github.com/saitddundar/vinctum-core/services/transfer/repository"
)

type fakeQuerier struct {
	transfers map[string]repository.Transfer
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{transfers: make(map[string]repository.Transfer)}
}

var errNotFound = pgx.ErrNoRows

func (f *fakeQuerier) CreateTransfer(_ context.Context, arg repository.CreateTransferParams) (repository.Transfer, error) {
	t := repository.Transfer{
		TransferID:     arg.TransferID,
		SenderNodeID:   arg.SenderNodeID,
		ReceiverNodeID: arg.ReceiverNodeID,
		Filename:       arg.Filename,
		TotalSizeBytes: arg.TotalSizeBytes,
		ContentHash:    arg.ContentHash,
		ChunkSizeBytes: arg.ChunkSizeBytes,
		TotalChunks:    arg.TotalChunks,
		ChunksDone:     0,
		Status:         arg.Status,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		EncryptionKey:  arg.EncryptionKey,
	}
	f.transfers[t.TransferID] = t
	return t, nil
}

func (f *fakeQuerier) GetTransfer(_ context.Context, transferID string) (repository.Transfer, error) {
	t, ok := f.transfers[transferID]
	if !ok {
		return repository.Transfer{}, errNotFound
	}
	return t, nil
}

func (f *fakeQuerier) ListTransfersByNode(_ context.Context, nodeID string) ([]repository.Transfer, error) {
	var result []repository.Transfer
	for _, t := range f.transfers {
		if t.SenderNodeID == nodeID || t.ReceiverNodeID == nodeID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (f *fakeQuerier) ListTransfersByStatus(_ context.Context, arg repository.ListTransfersByStatusParams) ([]repository.Transfer, error) {
	var result []repository.Transfer
	for _, t := range f.transfers {
		if (t.SenderNodeID == arg.SenderNodeID || t.ReceiverNodeID == arg.SenderNodeID) && t.Status == arg.Status {
			result = append(result, t)
		}
	}
	return result, nil
}

func (f *fakeQuerier) UpdateTransferProgress(_ context.Context, arg repository.UpdateTransferProgressParams) error {
	t, ok := f.transfers[arg.TransferID]
	if !ok {
		return errNotFound
	}
	t.ChunksDone = arg.ChunksDone
	t.UpdatedAt = time.Now()
	f.transfers[arg.TransferID] = t
	return nil
}

func (f *fakeQuerier) UpdateTransferStatus(_ context.Context, arg repository.UpdateTransferStatusParams) error {
	t, ok := f.transfers[arg.TransferID]
	if !ok {
		return errNotFound
	}
	t.Status = arg.Status
	t.UpdatedAt = time.Now()
	f.transfers[arg.TransferID] = t
	return nil
}

func (f *fakeQuerier) CompleteTransfer(_ context.Context, transferID string) error {
	t, ok := f.transfers[transferID]
	if !ok {
		return errNotFound
	}
	t.Status = 3
	t.ChunksDone = t.TotalChunks
	t.UpdatedAt = time.Now()
	f.transfers[transferID] = t
	return nil
}

// Compile-time interface check.
var _ repository.Querier = (*fakeQuerier)(nil)

// ──────────────────── helpers ────────────────────────────

func newTestHandler() *handler.TransferHandler {
	return handler.NewTransferHandler(newFakeQuerier(), nil, nil)
}

func initTransfer(t *testing.T, h *handler.TransferHandler) *transferv1.InitiateTransferResponse {
	t.Helper()
	resp, err := h.InitiateTransfer(context.Background(), &transferv1.InitiateTransferRequest{
		SenderNodeId:   "sender-1",
		ReceiverNodeId: "receiver-1",
		Filename:       "document.pdf",
		TotalSizeBytes: 1024 * 1024, // 1 MB
		ContentHash:    "sha256-abc123",
		ChunkSizeBytes: 256 * 1024, // 256 KB
	})
	require.NoError(t, err)
	return resp
}

// ──────────────────── tests ──────────────────────────────

func TestInitiateTransfer(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		resp := initTransfer(t, h)
		assert.NotEmpty(t, resp.TransferId)
		assert.Equal(t, int32(4), resp.TotalChunks) // 1MB / 256KB = 4
		assert.Equal(t, transferv1.TransferStatus_TRANSFER_STATUS_PENDING, resp.Status)
	})

	t.Run("missing sender", func(t *testing.T) {
		_, err := h.InitiateTransfer(ctx, &transferv1.InitiateTransferRequest{
			ReceiverNodeId: "receiver-1",
			TotalSizeBytes: 1024,
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("zero size", func(t *testing.T) {
		_, err := h.InitiateTransfer(ctx, &transferv1.InitiateTransferRequest{
			SenderNodeId:   "sender-1",
			ReceiverNodeId: "receiver-1",
			TotalSizeBytes: 0,
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("default chunk size", func(t *testing.T) {
		resp, err := h.InitiateTransfer(ctx, &transferv1.InitiateTransferRequest{
			SenderNodeId:   "sender-2",
			ReceiverNodeId: "receiver-2",
			TotalSizeBytes: 512 * 1024, // 512 KB
			// no ChunkSizeBytes → defaults to 256KB
		})
		require.NoError(t, err)
		assert.Equal(t, int32(2), resp.TotalChunks)
	})
}

func TestGetTransferStatus(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	resp := initTransfer(t, h)

	t.Run("success", func(t *testing.T) {
		sr, err := h.GetTransferStatus(ctx, &transferv1.GetTransferStatusRequest{
			TransferId: resp.TransferId,
		})
		require.NoError(t, err)
		assert.Equal(t, transferv1.TransferStatus_TRANSFER_STATUS_PENDING, sr.Status)
		assert.Equal(t, int32(4), sr.TotalChunks)
		assert.Equal(t, int32(0), sr.ChunksTransferred)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := h.GetTransferStatus(ctx, &transferv1.GetTransferStatusRequest{
			TransferId: "nonexistent",
		})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("missing id", func(t *testing.T) {
		_, err := h.GetTransferStatus(ctx, &transferv1.GetTransferStatusRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestListTransfers(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	initTransfer(t, h) // creates a transfer for sender-1 / receiver-1

	t.Run("list by sender", func(t *testing.T) {
		lr, err := h.ListTransfers(ctx, &transferv1.ListTransfersRequest{NodeId: "sender-1"})
		require.NoError(t, err)
		assert.Len(t, lr.Transfers, 1)
		assert.Equal(t, "document.pdf", lr.Transfers[0].Filename)
	})

	t.Run("list by receiver", func(t *testing.T) {
		lr, err := h.ListTransfers(ctx, &transferv1.ListTransfersRequest{NodeId: "receiver-1"})
		require.NoError(t, err)
		assert.Len(t, lr.Transfers, 1)
	})

	t.Run("empty list", func(t *testing.T) {
		lr, err := h.ListTransfers(ctx, &transferv1.ListTransfersRequest{NodeId: "unknown-node"})
		require.NoError(t, err)
		assert.Empty(t, lr.Transfers)
	})

	t.Run("missing node_id", func(t *testing.T) {
		_, err := h.ListTransfers(ctx, &transferv1.ListTransfersRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestCancelTransfer(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	resp := initTransfer(t, h)

	t.Run("success", func(t *testing.T) {
		cr, err := h.CancelTransfer(ctx, &transferv1.CancelTransferRequest{
			TransferId: resp.TransferId,
			Reason:     "user requested",
		})
		require.NoError(t, err)
		assert.True(t, cr.Success)

		// Verify status changed.
		sr, _ := h.GetTransferStatus(ctx, &transferv1.GetTransferStatusRequest{
			TransferId: resp.TransferId,
		})
		assert.Equal(t, transferv1.TransferStatus_TRANSFER_STATUS_CANCELLED, sr.Status)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := h.CancelTransfer(ctx, &transferv1.CancelTransferRequest{
			TransferId: "nonexistent",
		})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("missing id", func(t *testing.T) {
		_, err := h.CancelTransfer(ctx, &transferv1.CancelTransferRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}
