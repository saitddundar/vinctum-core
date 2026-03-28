package handler

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/internal/encryption"
	routingv1 "github.com/saitddundar/vinctum-core/proto/routing/v1"
	transferv1 "github.com/saitddundar/vinctum-core/proto/transfer/v1"
	"github.com/saitddundar/vinctum-core/services/transfer/repository"
	"github.com/saitddundar/vinctum-core/services/transfer/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultChunkSize = 256 * 1024 // 256 KB

// TransferHandler implements the TransferService gRPC server.
type TransferHandler struct {
	transferv1.UnimplementedTransferServiceServer
	queries       repository.Querier
	routingClient routingv1.RoutingServiceClient // optional — may be nil
	chunks        storage.ChunkStore             // optional — may be nil
}

func NewTransferHandler(q repository.Querier, rc routingv1.RoutingServiceClient, cs storage.ChunkStore) *TransferHandler {
	return &TransferHandler{queries: q, routingClient: rc, chunks: cs}
}

func (h *TransferHandler) InitiateTransfer(ctx context.Context, req *transferv1.InitiateTransferRequest) (*transferv1.InitiateTransferResponse, error) {
	if req.SenderNodeId == "" || req.ReceiverNodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "sender_node_id and receiver_node_id are required")
	}
	if req.TotalSizeBytes <= 0 {
		return nil, status.Error(codes.InvalidArgument, "total_size_bytes must be positive")
	}

	if h.routingClient != nil {
		routeResp, err := h.routingClient.FindRoute(ctx, &routingv1.FindRouteRequest{
			SourceNodeId: req.SenderNodeId,
			TargetNodeId: req.ReceiverNodeId,
			MaxHops:      10,
		})
		if err != nil {
			// Non-fatal: log and continue without route info.
			log.Warn().Err(err).
				Str("sender", req.SenderNodeId).
				Str("receiver", req.ReceiverNodeId).
				Msg("route resolution failed, proceeding without route")
		} else {
			log.Info().
				Int32("hops", routeResp.TotalHops).
				Int64("latency_ms", routeResp.EstimatedLatencyMs).
				Bool("direct", routeResp.DirectPossible).
				Msg("route resolved for transfer")
		}
	}

	chunkSize := int32(req.ChunkSizeBytes)
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}

	totalChunks := int32((req.TotalSizeBytes + int64(chunkSize) - 1) / int64(chunkSize))
	transferID := uuid.NewString()

	encKey := req.EncryptionKey
	if encKey != "" {
		if _, err := encryption.DecodeKey(encKey); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid encryption_key: %v", err)
		}
	}

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
		EncryptionKey:  encKey,
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

func (h *TransferHandler) SendChunk(stream transferv1.TransferService_SendChunkServer) error {
	var transferID string
	var chunksReceived int32
	var totalChunks int32
	var encKeyBytes []byte

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

			if t.EncryptionKey != "" {
				encKeyBytes, err = encryption.DecodeKey(t.EncryptionKey)
				if err != nil {
					return status.Errorf(codes.Internal, "invalid stored encryption key: %v", err)
				}
			}

			// Mark as in-progress on first chunk.
			_ = h.queries.UpdateTransferStatus(stream.Context(), repository.UpdateTransferStatusParams{
				TransferID: transferID,
				Status:     int32(transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS),
			})
		}

		// Encrypt chunk data before persisting if E2E key is set.
		dataToStore := chunk.Data
		if encKeyBytes != nil {
			dataToStore, err = encryption.Encrypt(encKeyBytes, chunk.Data)
			if err != nil {
				log.Error().Err(err).Str("transfer_id", transferID).Int32("chunk", chunk.ChunkIndex).Msg("failed to encrypt chunk")
				return status.Error(codes.Internal, "failed to encrypt chunk")
			}
		}

		// Persist chunk to storage backend.
		if h.chunks != nil {
			storedHash, err := h.chunks.SaveChunk(transferID, chunk.ChunkIndex, dataToStore)
			if err != nil {
				log.Error().Err(err).Str("transfer_id", transferID).Int32("chunk", chunk.ChunkIndex).Msg("failed to persist chunk")
				return status.Error(codes.Internal, "failed to persist chunk")
			}
			// Verify hash against the original (unencrypted) data if the client provided one.
			if chunk.ChunkHash != "" && encKeyBytes == nil && chunk.ChunkHash != storedHash {
				log.Warn().
					Str("expected", chunk.ChunkHash).
					Str("actual", storedHash).
					Msg("chunk hash mismatch")
				return status.Error(codes.DataLoss, "chunk hash mismatch")
			}
		}

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

	var encKeyBytes []byte
	if t.EncryptionKey != "" {
		encKeyBytes, err = encryption.DecodeKey(t.EncryptionKey)
		if err != nil {
			return status.Errorf(codes.Internal, "invalid stored encryption key: %v", err)
		}
	}

	startChunk := req.StartChunk
	if startChunk < 0 {
		startChunk = 0
	}

	for i := startChunk; i < t.TotalChunks; i++ {
		isLast := i == t.TotalChunks-1

		var data []byte
		var chunkHash string

		if h.chunks != nil {
			data, err = h.chunks.LoadChunk(t.TransferID, i)
			if err != nil {
				log.Error().Err(err).Str("transfer_id", t.TransferID).Int32("chunk", i).Msg("failed to load chunk")
				return status.Error(codes.Internal, "failed to load chunk from storage")
			}
			// Decrypt if E2E key is set.
			if encKeyBytes != nil {
				data, err = encryption.Decrypt(encKeyBytes, data)
				if err != nil {
					log.Error().Err(err).Str("transfer_id", t.TransferID).Int32("chunk", i).Msg("failed to decrypt chunk")
					return status.Error(codes.Internal, "failed to decrypt chunk")
				}
			}
		} else {
			chunkHash = fmt.Sprintf("chunk-%d-hash", i)
		}

		if err := stream.Send(&transferv1.DataChunk{
			TransferId: t.TransferID,
			ChunkIndex: i,
			Data:       data,
			ChunkHash:  chunkHash,
			IsLast:     isLast,
		}); err != nil {
			return status.Error(codes.Internal, "failed to send chunk")
		}
	}

	return nil
}

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
			TransferId:      t.TransferID,
			SenderNodeId:    t.SenderNodeID,
			ReceiverNodeId:  t.ReceiverNodeID,
			Filename:        t.Filename,
			TotalSizeBytes:  t.TotalSizeBytes,
			Status:          transferv1.TransferStatus(t.Status),
			ProgressPercent: progress,
			CreatedAt:       timestamppb.New(t.CreatedAt),
		})
	}

	return &transferv1.ListTransfersResponse{Transfers: transfers}, nil
}

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

	// Clean up stored chunks on cancellation.
	if h.chunks != nil {
		if delErr := h.chunks.DeleteTransfer(req.TransferId); delErr != nil {
			log.Warn().Err(delErr).Str("transfer_id", req.TransferId).Msg("failed to delete stored chunks")
		}
	}

	log.Info().Str("transfer_id", req.TransferId).Str("reason", req.Reason).Msg("transfer cancelled")

	return &transferv1.CancelTransferResponse{
		Success: true,
		Message: "transfer cancelled",
	}, nil
}
