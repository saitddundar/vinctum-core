package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"github.com/saitddundar/vinctum-core/internal/encryption"
	"github.com/saitddundar/vinctum-core/internal/relay"
	relayv1 "github.com/saitddundar/vinctum-core/proto/relay/v1"
	routingv1 "github.com/saitddundar/vinctum-core/proto/routing/v1"
	transferv1 "github.com/saitddundar/vinctum-core/proto/transfer/v1"
	"github.com/saitddundar/vinctum-core/services/transfer/repository"
	"github.com/saitddundar/vinctum-core/services/transfer/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultChunkSize = 256 * 1024 // 256 KB

type TransferHandler struct {
	transferv1.UnimplementedTransferServiceServer
	queries       repository.Querier
	routingClient routingv1.RoutingServiceClient // optional — may be nil
	chunks        storage.ChunkStore             // optional — may be nil
	relayClient   *relay.Client                  // optional — may be nil
	nodeID        string
}

func NewTransferHandler(q repository.Querier, rc routingv1.RoutingServiceClient, cs storage.ChunkStore, rc2 *relay.Client, nodeID string) *TransferHandler {
	return &TransferHandler{queries: q, routingClient: rc, chunks: cs, relayClient: rc2, nodeID: nodeID}
}

func (h *TransferHandler) InitiateTransfer(ctx context.Context, req *transferv1.InitiateTransferRequest) (*transferv1.InitiateTransferResponse, error) {
	if req.SenderNodeId == "" || req.ReceiverNodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "sender_node_id and receiver_node_id are required")
	}
	if req.TotalSizeBytes <= 0 {
		return nil, status.Error(codes.InvalidArgument, "total_size_bytes must be positive")
	}

	var routeHops []*routingv1.RouteHop
	if h.routingClient != nil {
		routeResp, err := h.routingClient.FindRoute(ctx, &routingv1.FindRouteRequest{
			SourceNodeId: req.SenderNodeId,
			TargetNodeId: req.ReceiverNodeId,
			MaxHops:      10,
		})
		if err != nil {
			log.Warn().Err(err).
				Str("sender", req.SenderNodeId).
				Str("receiver", req.ReceiverNodeId).
				Msg("route resolution failed, proceeding without route")
		} else {
			routeHops = routeResp.Hops
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

	routeJSON, _ := json.Marshal(routeHops)

	replicationFactor := req.ReplicationFactor
	if replicationFactor <= 0 {
		replicationFactor = 1
	}

	t, err := h.queries.CreateTransfer(ctx, repository.CreateTransferParams{
		TransferID:        transferID,
		SenderNodeID:      req.SenderNodeId,
		ReceiverNodeID:    req.ReceiverNodeId,
		Filename:          req.Filename,
		TotalSizeBytes:    req.TotalSizeBytes,
		ContentHash:       req.ContentHash,
		ChunkSizeBytes:    chunkSize,
		TotalChunks:       totalChunks,
		Status:            int32(transferv1.TransferStatus_TRANSFER_STATUS_PENDING),
		EncryptionKey:     encKey,
		RouteHops:         routeJSON,
		ReplicationFactor: replicationFactor,
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
		RouteHops:   routeHops,
	}, nil
}

func (h *TransferHandler) SendChunk(stream transferv1.TransferService_SendChunkServer) error {
	var transferID string
	var chunksReceived int32
	var totalChunks int32
	var encKeyBytes []byte
	var storedRouteHops []*routingv1.RouteHop
	var replicationFactor int32

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
			replicationFactor = t.ReplicationFactor

			if t.EncryptionKey != "" {
				encKeyBytes, err = encryption.DecodeKey(t.EncryptionKey)
				if err != nil {
					return status.Errorf(codes.Internal, "invalid stored encryption key: %v", err)
				}
			}

			// Deserialize stored route hops.
			if len(t.RouteHops) > 0 {
				_ = json.Unmarshal(t.RouteHops, &storedRouteHops)
			}

			_ = h.queries.UpdateTransferStatus(stream.Context(), repository.UpdateTransferStatusParams{
				TransferID: transferID,
				Status:     int32(transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS),
			})
		}

		// Verify chunk hash against plaintext data before encryption.
		if chunk.ChunkHash != "" {
			plaintextHash := sha256Hex(chunk.Data)
			if chunk.ChunkHash != plaintextHash {
				log.Warn().
					Str("expected", chunk.ChunkHash).
					Str("actual", plaintextHash).
					Msg("chunk hash mismatch")
				return status.Error(codes.DataLoss, "chunk hash mismatch")
			}
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

		// Always persist locally as the sender's copy.
		if h.chunks != nil {
			if _, err := h.chunks.SaveChunk(transferID, chunk.ChunkIndex, dataToStore); err != nil {
				log.Error().Err(err).Str("transfer_id", transferID).Int32("chunk", chunk.ChunkIndex).Msg("failed to persist chunk")
				return status.Error(codes.Internal, "failed to persist chunk")
			}
		}

		// Forward chunk along the route via relay if route hops exist.
		if h.relayClient != nil && len(storedRouteHops) > 0 {
			// Skip the first hop (sender = us), forward to remaining hops.
			remainingHops := h.filterSelfFromHops(storedRouteHops)

			if len(remainingHops) > 0 {
				encHash := sha256Hex(dataToStore)
				relayReq := &relayv1.RelayChunkRequest{
					TransferId:        transferID,
					ChunkIndex:        chunk.ChunkIndex,
					Data:              dataToStore,
					ChunkHash:         encHash,
					RemainingHops:     remainingHops,
					ReplicationFactor: replicationFactor,
					Ttl:               int32(len(remainingHops) + 2),
					EncryptionKey:     "", // Don't send key to relays — E2E
				}

				nextNode := remainingHops[0].NodeId
				resp, relayErr := h.relayClient.ForwardChunk(stream.Context(), nextNode, relayReq)
				if relayErr != nil {
					log.Warn().
						Err(relayErr).
						Str("transfer_id", transferID).
						Int32("chunk", chunk.ChunkIndex).
						Str("next_hop", nextNode).
						Msg("relay forwarding failed, chunk stored locally only")
				} else if resp != nil && resp.Success {
					log.Debug().
						Str("transfer_id", transferID).
						Int32("chunk", chunk.ChunkIndex).
						Str("relayed_to", resp.NodeId).
						Msg("chunk relayed successfully")
				}
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

	// Complete if all chunks received; verify content hash if provided.
	finalStatus := transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS
	if chunksReceived >= totalChunks {
		if err := h.verifyContentHash(stream.Context(), transferID, encKeyBytes); err != nil {
			_ = h.queries.UpdateTransferStatus(stream.Context(), repository.UpdateTransferStatusParams{
				TransferID: transferID,
				Status:     int32(transferv1.TransferStatus_TRANSFER_STATUS_FAILED),
			})
			return err
		}
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
			chunkHash = sha256Hex(data)
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

func (h *TransferHandler) verifyContentHash(ctx context.Context, transferID string, encKey []byte) error {
	if h.chunks == nil {
		return nil
	}
	t, err := h.queries.GetTransfer(ctx, transferID)
	if err != nil || t.ContentHash == "" {
		return nil // nothing to verify
	}

	hasher := sha256.New()
	for i := int32(0); i < t.TotalChunks; i++ {
		data, err := h.chunks.LoadChunk(transferID, i)
		if err != nil {
			return status.Error(codes.Internal, "failed to load chunk for verification")
		}
		if encKey != nil {
			data, err = encryption.Decrypt(encKey, data)
			if err != nil {
				return status.Error(codes.Internal, "failed to decrypt chunk for verification")
			}
		}
		hasher.Write(data)
	}

	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != t.ContentHash {
		log.Error().
			Str("transfer_id", transferID).
			Str("expected", t.ContentHash).
			Str("actual", actualHash).
			Msg("content hash mismatch after transfer")
		return status.Error(codes.DataLoss, "content hash mismatch")
	}
	return nil
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (h *TransferHandler) filterSelfFromHops(hops []*routingv1.RouteHop) []*routingv1.RouteHop {
	for i, hop := range hops {
		if hop.NodeId == h.nodeID {
			return hops[i+1:]
		}
	}
	// If sender not in hops, return all.
	return hops
}
