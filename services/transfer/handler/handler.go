package handler

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	transferv1 "github.com/saitddundar/vinctum-core/proto/transfer/v1"
	"github.com/saitddundar/vinctum-core/services/transfer/repository"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultChunkSize = 256 * 1024 // 256 KB

// TransferHandler implements the TransferService gRPC server.
type TransferHandler struct {
	transferv1.UnimplementedTransferServiceServer
	queries repository.Querier
}

// NewTransferHandler creates a new TransferHandler backed by the given Querier.
func NewTransferHandler(q repository.Querier) *TransferHandler {
	return &TransferHandler{queries: q}
}

// InitiateTransfer creates a new transfer session and returns the transfer ID.
func (h *TransferHandler) InitiateTransfer(ctx context.Context, req *transferv1.InitiateTransferRequest) (*transferv1.InitiateTransferResponse, error) {
	if req.SenderNodeId == "" || req.ReceiverNodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "sender_node_id and receiver_node_id are required")
	}
	if req.TotalSizeBytes <= 0 {
		return nil, status.Error(codes.InvalidArgument, "total_size_bytes must be positive")
	}

	chunkSize := int32(req.ChunkSizeBytes)
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}

	totalChunks := int32((req.TotalSizeBytes + int64(chunkSize) - 1) / int64(chunkSize))
	transferID := uuid.NewString()

	t, err := h.queries.CreateTransfer(ctx, repository.CreateTransferParams{
		TransferID:     transferID,
		SenderNodeID:   req.SenderNodeId,
		ReceiverNodeID: req.ReceiverNodeId,
		Filename:       req.Filename,
		TotalSizeBytes: req.TotalSizeBytes,
		ContentHash:    req.ContentHash,
		ChunkSizeBytes: chunkSize,
		TotalChunks:    totalChunks,
		Status:         int32(transferv1.TransferStatus_TRANSFER_STATUS_PENDING),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create transfer")
	}

	log.Info().
		Str("transfer_id", transferID).
		Str("sender", req.SenderNodeId).
		Str("receiver", req.ReceiverNodeId).
		Int64("size", req.TotalSizeBytes).
		Msg("transfer initiated")

	return &transferv1.InitiateTransferResponse{
		TransferId:  t.TransferID,
		TotalChunks: totalChunks,
		Status:      transferv1.TransferStatus_TRANSFER_STATUS_PENDING,
		CreatedAt:   timestamppb.New(t.CreatedAt),
	}, nil
}

// SendChunk receives a stream of chunks from the sender and updates progress.
func (h *TransferHandler) SendChunk(stream transferv1.TransferService_SendChunkServer) error {
	var transferID string
	var chunksReceived int32
	var totalChunks int32

	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return status.Error(codes.Internal, "failed to receive chunk")
		}

		if transferID == "" {
			transferID = chunk.TransferId

			t, err := h.queries.GetTransfer(stream.Context(), transferID)
			if err != nil {
				return status.Error(codes.NotFound, "transfer not found")
			}
			totalChunks = t.TotalChunks

			// Mark as in-progress on first chunk.
			_ = h.queries.UpdateTransferStatus(stream.Context(), repository.UpdateTransferStatusParams{
				TransferID: transferID,
				Status:     int32(transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS),
			})
		}

		// TODO: persist chunk data to object store or local disk.
		chunksReceived = chunk.ChunkIndex + 1

		_ = h.queries.UpdateTransferProgress(stream.Context(), repository.UpdateTransferProgressParams{
			TransferID: transferID,
			ChunksDone: chunksReceived,
		})

		log.Debug().
			Str("transfer_id", transferID).
			Int32("chunk", chunk.ChunkIndex).
			Msg("chunk received")
	}

	// Complete if all chunks received.
	finalStatus := transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS
	if chunksReceived >= totalChunks {
		_ = h.queries.CompleteTransfer(stream.Context(), transferID)
		finalStatus = transferv1.TransferStatus_TRANSFER_STATUS_COMPLETED
	}

	log.Info().
		Str("transfer_id", transferID).
		Int32("chunks_received", chunksReceived).
		Msg("send stream ended")

	return stream.SendAndClose(&transferv1.SendChunkResponse{
		TransferId:     transferID,
		ChunksReceived: chunksReceived,
		TotalChunks:    totalChunks,
		Status:         finalStatus,
	})
}

// ReceiveChunks streams chunks to the receiver for a given transfer.
func (h *TransferHandler) ReceiveChunks(req *transferv1.ReceiveChunksRequest, stream transferv1.TransferService_ReceiveChunksServer) error {
	if req.TransferId == "" {
		return status.Error(codes.InvalidArgument, "transfer_id is required")
	}

	t, err := h.queries.GetTransfer(stream.Context(), req.TransferId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return status.Error(codes.NotFound, "transfer not found")
		}
		return status.Error(codes.Internal, "failed to fetch transfer")
	}

	startChunk := req.StartChunk
	if startChunk < 0 {
		startChunk = 0
	}

	// TODO: read actual chunk data from object store or local disk.
	// For now, send placeholder metadata chunks so that the gRPC contract works.
	for i := startChunk; i < t.TotalChunks; i++ {
		isLast := i == t.TotalChunks-1
		if err := stream.Send(&transferv1.DataChunk{
			TransferId: t.TransferID,
			ChunkIndex: i,
			Data:       nil, // placeholder — actual data comes from storage backend
			ChunkHash:  fmt.Sprintf("chunk-%d-hash", i),
			IsLast:     isLast,
		}); err != nil {
			return status.Error(codes.Internal, "failed to send chunk")
		}
	}

	return nil
}

// GetTransferStatus returns the current state of a transfer.
func (h *TransferHandler) GetTransferStatus(ctx context.Context, req *transferv1.GetTransferStatusRequest) (*transferv1.GetTransferStatusResponse, error) {
	if req.TransferId == "" {
		return nil, status.Error(codes.InvalidArgument, "transfer_id is required")
	}

	t, err := h.queries.GetTransfer(ctx, req.TransferId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "transfer not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch transfer")
	}

	bytesTransferred := int64(t.ChunksDone) * int64(t.ChunkSizeBytes)
	if bytesTransferred > t.TotalSizeBytes {
		bytesTransferred = t.TotalSizeBytes
	}

	return &transferv1.GetTransferStatusResponse{
		TransferId:        t.TransferID,
		Status:            transferv1.TransferStatus(t.Status),
		ChunksTransferred: t.ChunksDone,
		TotalChunks:       t.TotalChunks,
		BytesTransferred:  bytesTransferred,
		TotalBytes:        t.TotalSizeBytes,
		StartedAt:         timestamppb.New(t.CreatedAt),
		UpdatedAt:         timestamppb.New(t.UpdatedAt),
	}, nil
}

// ListTransfers returns all transfers associated with a node, optionally
// filtered by status.
func (h *TransferHandler) ListTransfers(ctx context.Context, req *transferv1.ListTransfersRequest) (*transferv1.ListTransfersResponse, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}

	var rows []repository.Transfer
	var err error

	if req.FilterStatus != transferv1.TransferStatus_TRANSFER_STATUS_UNSPECIFIED {
		rows, err = h.queries.ListTransfersByStatus(ctx, repository.ListTransfersByStatusParams{
			SenderNodeID: req.NodeId,
			Status:       int32(req.FilterStatus),
		})
	} else {
		rows, err = h.queries.ListTransfersByNode(ctx, req.NodeId)
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list transfers")
	}

	transfers := make([]*transferv1.TransferInfo, 0, len(rows))
	for _, t := range rows {
		progress := int32(0)
		if t.TotalChunks > 0 {
			progress = (t.ChunksDone * 100) / t.TotalChunks
		}
		transfers = append(transfers, &transferv1.TransferInfo{
			TransferId:     t.TransferID,
			SenderNodeId:   t.SenderNodeID,
			ReceiverNodeId: t.ReceiverNodeID,
			Filename:       t.Filename,
			TotalSizeBytes: t.TotalSizeBytes,
			Status:         transferv1.TransferStatus(t.Status),
			ProgressPercent: progress,
			CreatedAt:      timestamppb.New(t.CreatedAt),
		})
	}

	return &transferv1.ListTransfersResponse{Transfers: transfers}, nil
}

// CancelTransfer marks a transfer as cancelled.
func (h *TransferHandler) CancelTransfer(ctx context.Context, req *transferv1.CancelTransferRequest) (*transferv1.CancelTransferResponse, error) {
	if req.TransferId == "" {
		return nil, status.Error(codes.InvalidArgument, "transfer_id is required")
	}

	_, err := h.queries.GetTransfer(ctx, req.TransferId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "transfer not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch transfer")
	}

	if err := h.queries.UpdateTransferStatus(ctx, repository.UpdateTransferStatusParams{
		TransferID: req.TransferId,
		Status:     int32(transferv1.TransferStatus_TRANSFER_STATUS_CANCELLED),
	}); err != nil {
		return nil, status.Error(codes.Internal, "failed to cancel transfer")
	}

	log.Info().Str("transfer_id", req.TransferId).Str("reason", req.Reason).Msg("transfer cancelled")

	return &transferv1.CancelTransferResponse{
		Success: true,
		Message: "transfer cancelled",
	}, nil
}
