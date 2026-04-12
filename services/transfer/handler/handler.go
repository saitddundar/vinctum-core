package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
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

	// E2E enforcement: encryption keys must never reach the server. Clients
	// must encrypt chunks locally before SendChunk and decrypt locally after
	// ReceiveChunks. The encryption_key field is kept in the proto only for
	// backwards compatibility and is rejected here.
	if req.EncryptionKey != "" {
		return nil, status.Error(codes.InvalidArgument,
			"encryption_key must not be sent to server; encrypt chunks client-side")
	}

	if len(req.SenderEphemeralPubkey) != 32 {
		return nil, status.Error(codes.InvalidArgument,
			"sender_ephemeral_pubkey must be exactly 32 bytes (X25519 public key)")
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
		EncryptionKey:     "", // never stored; chunks are E2E encrypted client-side
		RouteHops:         routeJSON,
		ReplicationFactor:     replicationFactor,
		SenderEphemeralPubkey: req.SenderEphemeralPubkey,
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
		TransferId:            t.TransferID,
		TotalChunks:           totalChunks,
		Status:                transferv1.TransferStatus_TRANSFER_STATUS_PENDING,
		CreatedAt:             timestamppb.New(t.CreatedAt),
		RouteHops:             routeHops,
		SenderEphemeralPubkey: t.SenderEphemeralPubkey,
	}, nil
}

func (h *TransferHandler) SendChunk(stream transferv1.TransferService_SendChunkServer) error {
	var transferID string
	var chunksReceived int32
	var totalChunks int32
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

			// Deserialize stored route hops.
			if len(t.RouteHops) > 0 {
				_ = json.Unmarshal(t.RouteHops, &storedRouteHops)
			}

			_ = h.queries.UpdateTransferStatus(stream.Context(), repository.UpdateTransferStatusParams{
				TransferID: transferID,
				Status:     int32(transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS),
			})
		}

		// Verify transport integrity: chunk_hash must match the bytes we
		// actually received. Chunks are E2E encrypted by the client, so this
		// hash is over the ciphertext, not plaintext.
		if chunk.ChunkHash == "" {
			return status.Error(codes.InvalidArgument, "chunk_hash is required for integrity verification")
		}
		if h := sha256Hex(chunk.Data); h != chunk.ChunkHash {
			log.Warn().
				Str("expected", chunk.ChunkHash).
				Str("actual", h).
				Msg("chunk hash mismatch")
			return status.Error(codes.DataLoss, "chunk hash mismatch")
		}

		// Persist the opaque ciphertext as the sender's copy.
		if h.chunks != nil {
			if _, err := h.chunks.SaveChunk(transferID, chunk.ChunkIndex, chunk.Data); err != nil {
				log.Error().Err(err).Str("transfer_id", transferID).Int32("chunk", chunk.ChunkIndex).Msg("failed to persist chunk")
				return status.Error(codes.Internal, "failed to persist chunk")
			}
		}

		// Forward ciphertext along the route via relay if route hops exist.
		if h.relayClient != nil && len(storedRouteHops) > 0 {
			// Skip the first hop (sender = us), forward to remaining hops.
			remainingHops := h.filterSelfFromHops(storedRouteHops)

			if len(remainingHops) > 0 {
				relayReq := &relayv1.RelayChunkRequest{
					TransferId:        transferID,
					ChunkIndex:        chunk.ChunkIndex,
					Data:              chunk.Data,
					ChunkHash:         chunk.ChunkHash,
					RemainingHops:     remainingHops,
					ReplicationFactor: replicationFactor,
					Ttl:               int32(len(remainingHops) + 2),
					EncryptionKey:     "", // never sent — E2E
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

	// Complete if all chunks received. The plaintext content hash cannot be
	// verified by the server because chunks are E2E encrypted; the receiver
	// verifies it after decrypting the full payload.
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
			// Hash is over the opaque ciphertext; the receiver decrypts and
			// verifies the plaintext content hash locally.
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
		TransferId:            t.TransferID,
		Status:                transferv1.TransferStatus(t.Status),
		ChunksTransferred:     t.ChunksDone,
		TotalChunks:           t.TotalChunks,
		BytesTransferred:      bytesTransferred,
		TotalBytes:            t.TotalSizeBytes,
		StartedAt:             timestamppb.New(t.CreatedAt),
		UpdatedAt:             timestamppb.New(t.UpdatedAt),
		SenderEphemeralPubkey: t.SenderEphemeralPubkey,
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
			TransferId:            t.TransferID,
			SenderNodeId:          t.SenderNodeID,
			ReceiverNodeId:        t.ReceiverNodeID,
			Filename:              t.Filename,
			TotalSizeBytes:        t.TotalSizeBytes,
			Status:                transferv1.TransferStatus(t.Status),
			ProgressPercent:       progress,
			CreatedAt:             timestamppb.New(t.CreatedAt),
			SenderEphemeralPubkey: t.SenderEphemeralPubkey,
			ContentHash:           t.ContentHash,
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

// WatchTransfers streams transfer events for a given node so the receiver can
// react to incoming transfers without polling. The implementation polls the
// database on a short interval and emits diffs (NEW / UPDATED / COMPLETED /
// CANCELLED). This mirrors the StreamPeerUpdates pattern in the discovery
// service and avoids the need for a separate pub/sub broker.
func (h *TransferHandler) WatchTransfers(req *transferv1.WatchTransfersRequest, stream transferv1.TransferService_WatchTransfersServer) error {
	if req.NodeId == "" {
		return status.Error(codes.InvalidArgument, "node_id is required")
	}

	type snapshot struct {
		status     int32
		chunksDone int32
	}
	known := make(map[string]snapshot)
	first := true

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	emit := func(t repository.Transfer, eventType transferv1.TransferEvent_EventType) error {
		if req.ReceiverOnly && t.ReceiverNodeID != req.NodeId {
			return nil
		}
		progress := int32(0)
		if t.TotalChunks > 0 {
			progress = (t.ChunksDone * 100) / t.TotalChunks
		}
		return stream.Send(&transferv1.TransferEvent{
			Type: eventType,
			Transfer: &transferv1.TransferInfo{
				TransferId:            t.TransferID,
				SenderNodeId:          t.SenderNodeID,
				ReceiverNodeId:        t.ReceiverNodeID,
				Filename:              t.Filename,
				TotalSizeBytes:        t.TotalSizeBytes,
				Status:                transferv1.TransferStatus(t.Status),
				ProgressPercent:       progress,
				CreatedAt:             timestamppb.New(t.CreatedAt),
				SenderEphemeralPubkey: t.SenderEphemeralPubkey,
				ContentHash:           t.ContentHash,
			},
			Timestamp: timestamppb.Now(),
		})
	}

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
			rows, err := h.queries.ListTransfersByNode(stream.Context(), req.NodeId)
			if err != nil {
				log.Warn().Err(err).Str("node_id", req.NodeId).Msg("watch transfers query failed")
				continue
			}

			current := make(map[string]snapshot, len(rows))
			for _, t := range rows {
				current[t.TransferID] = snapshot{status: t.Status, chunksDone: t.ChunksDone}

				prev, exists := known[t.TransferID]
				if !exists {
					// On the very first tick, send each transfer as NEW so a
					// freshly subscribed receiver can backfill its inbox.
					if err := emit(t, transferv1.TransferEvent_EVENT_TYPE_NEW); err != nil {
						return err
					}
					continue
				}

				if prev.status == t.Status && prev.chunksDone == t.ChunksDone {
					continue
				}

				eventType := transferv1.TransferEvent_EVENT_TYPE_UPDATED
				switch transferv1.TransferStatus(t.Status) {
				case transferv1.TransferStatus_TRANSFER_STATUS_COMPLETED:
					eventType = transferv1.TransferEvent_EVENT_TYPE_COMPLETED
				case transferv1.TransferStatus_TRANSFER_STATUS_CANCELLED,
					transferv1.TransferStatus_TRANSFER_STATUS_FAILED:
					eventType = transferv1.TransferEvent_EVENT_TYPE_CANCELLED
				}

				if err := emit(t, eventType); err != nil {
					return err
				}
			}

			known = current
			_ = first
			first = false
		}
	}
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
