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
	"github.com/jackc/pgx/v5/pgtype"
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

// P2PTransferer provides direct peer-to-peer transfer capabilities.
// When non-nil, the transfer service can offer P2P as an alternative to relay.
type P2PTransferer interface {
	// SendFile sends all chunks directly to a peer via libp2p stream.
	SendFile(ctx context.Context, peerID string, transferID string, totalChunks int32, totalSizeBytes int64, contentHash string, chunkReader func(index int32) ([]byte, string, error)) error
	// IsReachable checks if a peer is directly reachable.
	IsReachable(ctx context.Context, peerID string) bool
	// PeerID returns this node's libp2p peer ID string.
	PeerID() string
	// Addrs returns this node's listening multiaddrs.
	Addrs() []string
}

type TransferHandler struct {
	transferv1.UnimplementedTransferServiceServer
	queries       repository.Querier
	routingClient routingv1.RoutingServiceClient // optional — may be nil
	chunks        storage.ChunkStore             // optional — may be nil
	relayClient   *relay.Client                  // optional — may be nil
	p2p           P2PTransferer                  // optional — may be nil
	nodeID        string
}

func NewTransferHandler(q repository.Querier, rc routingv1.RoutingServiceClient, cs storage.ChunkStore, rc2 *relay.Client, nodeID string) *TransferHandler {
	return &TransferHandler{queries: q, routingClient: rc, chunks: cs, relayClient: rc2, nodeID: nodeID}
}

// SetP2P injects the P2P transfer layer. Call after construction.
func (h *TransferHandler) SetP2P(p P2PTransferer) {
	h.p2p = p
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

	initialStatus := transferv1.TransferStatus_TRANSFER_STATUS_PENDING
	if !req.AutoApprove {
		initialStatus = transferv1.TransferStatus_TRANSFER_STATUS_AWAITING_APPROVAL
	}

	// Ensure non-nil byte slices to avoid SQL NULL on NOT NULL columns.
	if routeJSON == nil {
		routeJSON = []byte("[]")
	}
	t, err := h.queries.CreateTransfer(ctx, repository.CreateTransferParams{
		TransferID:            transferID,
		SenderNodeID:          req.SenderNodeId,
		ReceiverNodeID:        req.ReceiverNodeId,
		Filename:              req.Filename,
		TotalSizeBytes:        req.TotalSizeBytes,
		ContentHash:           req.ContentHash,
		ChunkSizeBytes:        chunkSize,
		TotalChunks:           totalChunks,
		Status:                int32(initialStatus),
		EncryptionKey:         "", // never stored; chunks are E2E encrypted client-side
		RouteHops:             routeJSON,
		ReplicationFactor:     replicationFactor,
		SenderEphemeralPubkey: req.SenderEphemeralPubkey,
		WrappedFileKey:        []byte{}, // no server-side key escrow; set to empty
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

	resp := &transferv1.InitiateTransferResponse{
		TransferId:            t.TransferID,
		TotalChunks:           totalChunks,
		Status:                initialStatus,
		CreatedAt:             timestamppb.New(t.CreatedAt),
		RouteHops:             routeHops,
		SenderEphemeralPubkey: t.SenderEphemeralPubkey,
		TransferMode:          transferv1.TransferMode_TRANSFER_MODE_RELAY,
	}

	// If P2P is available, include receiver peer info so the client can
	// attempt a direct connection before falling back to relay.
	if h.p2p != nil {
		resp.TransferMode = transferv1.TransferMode_TRANSFER_MODE_P2P_DIRECT
		resp.ReceiverPeerId = h.p2p.PeerID()
		resp.ReceiverAddrs = h.p2p.Addrs()
	}

	return resp, nil
}

func (h *TransferHandler) SendChunk(stream transferv1.TransferService_SendChunkServer) error {
	var transferID string
	var storageID string // group_transfer_id if group, else transfer_id
	var chunksReceived int32
	var totalChunks int32
	var storedRouteHops []*routingv1.RouteHop
	var replicationFactor int32
	var isGroup bool
	var siblingTransferIDs []string // other transfers in the group

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

			// Check if this is a group transfer ID first.
			gt, gtErr := h.queries.GetGroupTransfer(stream.Context(), transferID)
			if gtErr == nil {
				// Uploading to a group transfer directly.
				isGroup = true
				storageID = gt.ID
				totalChunks = gt.TotalChunks
				replicationFactor = 1

				siblings, _ := h.queries.ListTransfersByGroupID(stream.Context(), pgText(gt.ID))
				for _, s := range siblings {
					siblingTransferIDs = append(siblingTransferIDs, s.TransferID)
					_ = h.queries.UpdateTransferStatus(stream.Context(), repository.UpdateTransferStatusParams{
						TransferID: s.TransferID,
						Status:     int32(transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS),
					})
				}
			} else {
				// Regular 1:1 transfer.
				t, err := h.queries.GetTransfer(stream.Context(), transferID)
				if err != nil {
					return status.Error(codes.NotFound, "transfer not found")
				}
				if t.Status == int32(transferv1.TransferStatus_TRANSFER_STATUS_AWAITING_APPROVAL) {
					return status.Error(codes.FailedPrecondition, "transfer is awaiting receiver approval")
				}
				if t.Status == int32(transferv1.TransferStatus_TRANSFER_STATUS_PAUSED) {
					return status.Error(codes.FailedPrecondition, "transfer is paused")
				}
				storageID = t.TransferID
				totalChunks = t.TotalChunks
				replicationFactor = t.ReplicationFactor

				if t.GroupTransferID.Valid {
					storageID = t.GroupTransferID.String
				}

				// Deserialize stored route hops.
				if len(t.RouteHops) > 0 {
					_ = json.Unmarshal(t.RouteHops, &storedRouteHops)
				}
			}

			if !isGroup {
				_ = h.queries.UpdateTransferStatus(stream.Context(), repository.UpdateTransferStatusParams{
					TransferID: transferID,
					Status:     int32(transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS),
				})
			}
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

		// Persist the opaque ciphertext. For group transfers, store under the
		// group_transfer_id so all recipients share the same chunk storage.
		if h.chunks != nil {
			if _, err := h.chunks.SaveChunk(storageID, chunk.ChunkIndex, chunk.Data); err != nil {
				log.Error().Err(err).Str("storage_id", storageID).Int32("chunk", chunk.ChunkIndex).Msg("failed to persist chunk")
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

		// Update progress on all related transfers.
		if isGroup {
			for _, sid := range siblingTransferIDs {
				_ = h.queries.UpdateTransferProgress(stream.Context(), repository.UpdateTransferProgressParams{
					TransferID: sid,
					ChunksDone: chunksReceived,
				})
			}
		} else {
			_ = h.queries.UpdateTransferProgress(stream.Context(), repository.UpdateTransferProgressParams{
				TransferID: transferID,
				ChunksDone: chunksReceived,
			})
		}

		log.Debug().
			Str("storage_id", storageID).
			Int32("chunk", chunk.ChunkIndex).
			Msg("chunk received")
	}

	// Complete if all chunks received.
	finalStatus := transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS
	if chunksReceived >= totalChunks {
		if isGroup {
			for _, sid := range siblingTransferIDs {
				_ = h.queries.CompleteTransfer(stream.Context(), sid)
			}
		} else {
			_ = h.queries.CompleteTransfer(stream.Context(), transferID)
		}
		finalStatus = transferv1.TransferStatus_TRANSFER_STATUS_COMPLETED
	}

	log.Info().
		Str("storage_id", storageID).
		Int32("chunks_received", chunksReceived).
		Bool("group", isGroup).
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

	// For group transfers, chunks are stored under the group_transfer_id.
	chunkStorageID := t.TransferID
	if t.GroupTransferID.Valid {
		chunkStorageID = t.GroupTransferID.String
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
			data, err = h.chunks.LoadChunk(chunkStorageID, i)
			if err != nil {
				log.Error().Err(err).Str("storage_id", chunkStorageID).Int32("chunk", i).Msg("failed to load chunk")
				return status.Error(codes.Internal, "failed to load chunk from storage")
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

	resp := &transferv1.GetTransferStatusResponse{
		TransferId:            t.TransferID,
		Status:                transferv1.TransferStatus(t.Status),
		ChunksTransferred:     t.ChunksDone,
		TotalChunks:           t.TotalChunks,
		BytesTransferred:      bytesTransferred,
		TotalBytes:            t.TotalSizeBytes,
		StartedAt:             timestamppb.New(t.CreatedAt),
		UpdatedAt:             timestamppb.New(t.UpdatedAt),
		SenderEphemeralPubkey: t.SenderEphemeralPubkey,
		WrappedFileKey:        t.WrappedFileKey,
	}
	if t.GroupTransferID.Valid {
		resp.GroupTransferId = t.GroupTransferID.String
	}
	return resp, nil
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
		info := &transferv1.TransferInfo{
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
			WrappedFileKey:        t.WrappedFileKey,
		}
		if t.GroupTransferID.Valid {
			info.GroupTransferId = t.GroupTransferID.String
		}
		transfers = append(transfers, info)
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

func (h *TransferHandler) PauseTransfer(ctx context.Context, req *transferv1.PauseTransferRequest) (*transferv1.PauseTransferResponse, error) {
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

	if t.Status != int32(transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS) {
		return nil, status.Error(codes.FailedPrecondition, "only in-progress transfers can be paused")
	}

	if err := h.queries.UpdateTransferStatus(ctx, repository.UpdateTransferStatusParams{
		TransferID: req.TransferId,
		Status:     int32(transferv1.TransferStatus_TRANSFER_STATUS_PAUSED),
	}); err != nil {
		return nil, status.Error(codes.Internal, "failed to pause transfer")
	}

	log.Info().Str("transfer_id", req.TransferId).Msg("transfer paused")

	return &transferv1.PauseTransferResponse{
		TransferId: req.TransferId,
		Status:     transferv1.TransferStatus_TRANSFER_STATUS_PAUSED,
	}, nil
}

func (h *TransferHandler) ResumeTransfer(ctx context.Context, req *transferv1.ResumeTransferRequest) (*transferv1.ResumeTransferResponse, error) {
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

	if t.Status != int32(transferv1.TransferStatus_TRANSFER_STATUS_PAUSED) {
		return nil, status.Error(codes.FailedPrecondition, "only paused transfers can be resumed")
	}

	if err := h.queries.UpdateTransferStatus(ctx, repository.UpdateTransferStatusParams{
		TransferID: req.TransferId,
		Status:     int32(transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS),
	}); err != nil {
		return nil, status.Error(codes.Internal, "failed to resume transfer")
	}

	log.Info().Str("transfer_id", req.TransferId).Msg("transfer resumed")

	return &transferv1.ResumeTransferResponse{
		TransferId: req.TransferId,
		Status:     transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS,
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
				case transferv1.TransferStatus_TRANSFER_STATUS_PAUSED:
					eventType = transferv1.TransferEvent_EVENT_TYPE_PAUSED
				case transferv1.TransferStatus_TRANSFER_STATUS_IN_PROGRESS:
					if transferv1.TransferStatus(prev.status) == transferv1.TransferStatus_TRANSFER_STATUS_PAUSED {
						eventType = transferv1.TransferEvent_EVENT_TYPE_RESUMED
					}
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

func (h *TransferHandler) GetP2PConnectionInfo(ctx context.Context, req *transferv1.GetP2PConnectionInfoRequest) (*transferv1.GetP2PConnectionInfoResponse, error) {
	if req.TransferId == "" {
		return nil, status.Error(codes.InvalidArgument, "transfer_id is required")
	}

	if _, err := h.queries.GetTransfer(ctx, req.TransferId); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "transfer not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch transfer")
	}

	resp := &transferv1.GetP2PConnectionInfoResponse{}

	if h.p2p != nil {
		resp.ReceiverPeerId = h.p2p.PeerID()
		resp.ReceiverAddrs = h.p2p.Addrs()
		resp.DirectReachable = true
	}

	return resp, nil
}

func (h *TransferHandler) ConfirmP2PTransfer(ctx context.Context, req *transferv1.ConfirmP2PTransferRequest) (*transferv1.ConfirmP2PTransferResponse, error) {
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

	var finalStatus transferv1.TransferStatus
	var mode int32

	if req.Success {
		finalStatus = transferv1.TransferStatus_TRANSFER_STATUS_COMPLETED
		mode = int32(transferv1.TransferMode_TRANSFER_MODE_P2P_DIRECT)
	} else {
		// P2P failed — keep current status so client can fall back to relay.
		finalStatus = transferv1.TransferStatus(t.Status)
		mode = int32(transferv1.TransferMode_TRANSFER_MODE_RELAY)
		log.Warn().
			Str("transfer_id", req.TransferId).
			Str("error", req.ErrorMessage).
			Msg("p2p transfer failed, client should fallback to relay")
	}

	if err := h.queries.ConfirmP2PTransfer(ctx, repository.ConfirmP2PTransferParams{
		TransferID:   req.TransferId,
		Status:       int32(finalStatus),
		ChunksDone:   req.ChunksTransferred,
		TransferMode: mode,
	}); err != nil {
		return nil, status.Error(codes.Internal, "failed to update transfer")
	}

	return &transferv1.ConfirmP2PTransferResponse{
		Success: req.Success,
		Status:  finalStatus,
	}, nil
}

func (h *TransferHandler) RespondToTransfer(ctx context.Context, req *transferv1.RespondToTransferRequest) (*transferv1.RespondToTransferResponse, error) {
	if req.TransferId == "" || req.ReceiverNodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "transfer_id and receiver_node_id are required")
	}

	t, err := h.queries.GetTransfer(ctx, req.TransferId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "transfer not found")
	}
	if t.ReceiverNodeID != req.ReceiverNodeId {
		return nil, status.Error(codes.PermissionDenied, "you are not the receiver of this transfer")
	}
	if t.Status != int32(transferv1.TransferStatus_TRANSFER_STATUS_AWAITING_APPROVAL) {
		return nil, status.Error(codes.FailedPrecondition, "transfer is not awaiting approval")
	}

	var newStatus transferv1.TransferStatus
	if req.Accept {
		newStatus = transferv1.TransferStatus_TRANSFER_STATUS_PENDING
	} else {
		newStatus = transferv1.TransferStatus_TRANSFER_STATUS_CANCELLED
	}

	if err := h.queries.UpdateTransferStatus(ctx, repository.UpdateTransferStatusParams{
		TransferID: req.TransferId,
		Status:     int32(newStatus),
	}); err != nil {
		return nil, status.Error(codes.Internal, "failed to update transfer status")
	}

	log.Info().
		Str("transfer_id", req.TransferId).
		Bool("accepted", req.Accept).
		Msg("transfer response recorded")

	return &transferv1.RespondToTransferResponse{
		TransferId: req.TransferId,
		Status:     newStatus,
	}, nil
}

func (h *TransferHandler) InitiateGroupTransfer(ctx context.Context, req *transferv1.InitiateGroupTransferRequest) (*transferv1.InitiateGroupTransferResponse, error) {
	if req.SessionId == "" || req.SenderNodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id and sender_node_id are required")
	}
	if req.TotalSizeBytes <= 0 {
		return nil, status.Error(codes.InvalidArgument, "total_size_bytes must be positive")
	}
	if len(req.SenderEphemeralPubkey) != 32 {
		return nil, status.Error(codes.InvalidArgument, "sender_ephemeral_pubkey must be exactly 32 bytes")
	}
	if len(req.RecipientKeys) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one recipient_key is required")
	}

	chunkSize := int32(req.ChunkSizeBytes)
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	totalChunks := int32((req.TotalSizeBytes + int64(chunkSize) - 1) / int64(chunkSize))
	groupID := uuid.NewString()

	sessionUUID, err := uuid.Parse(req.SessionId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid session_id format")
	}

	gt, err := h.queries.CreateGroupTransfer(ctx, repository.CreateGroupTransferParams{
		ID:                    groupID,
		SessionID:             pgUUID(sessionUUID),
		SenderNodeID:          req.SenderNodeId,
		Filename:              req.Filename,
		TotalSizeBytes:        req.TotalSizeBytes,
		ContentHash:           req.ContentHash,
		ChunkSizeBytes:        chunkSize,
		TotalChunks:           totalChunks,
		SenderEphemeralPubkey: req.SenderEphemeralPubkey,
	})
	if err != nil {
		log.Error().Err(err).Msg("failed to create group transfer")
		return nil, status.Error(codes.Internal, "failed to create group transfer")
	}

	transfers := make([]*transferv1.TransferInfo, 0, len(req.RecipientKeys))
	for _, rk := range req.RecipientKeys {
		if rk.ReceiverNodeId == "" || len(rk.WrappedFileKey) == 0 {
			return nil, status.Error(codes.InvalidArgument, "each recipient_key must have receiver_node_id and wrapped_file_key")
		}

		transferID := uuid.NewString()
		t, err := h.queries.CreateTransfer(ctx, repository.CreateTransferParams{
			TransferID:            transferID,
			SenderNodeID:          req.SenderNodeId,
			ReceiverNodeID:        rk.ReceiverNodeId,
			Filename:              req.Filename,
			TotalSizeBytes:        req.TotalSizeBytes,
			ContentHash:           req.ContentHash,
			ChunkSizeBytes:        chunkSize,
			TotalChunks:           totalChunks,
			Status:                int32(transferv1.TransferStatus_TRANSFER_STATUS_AWAITING_APPROVAL),
			EncryptionKey:         "",
			RouteHops:             []byte("[]"),
			ReplicationFactor:     1,
			SenderEphemeralPubkey: req.SenderEphemeralPubkey,
			GroupTransferID:       pgText(groupID),
			WrappedFileKey:        rk.WrappedFileKey,
		})
		if err != nil {
			log.Error().Err(err).Str("receiver", rk.ReceiverNodeId).Msg("failed to create recipient transfer")
			return nil, status.Error(codes.Internal, "failed to create recipient transfer")
		}

		transfers = append(transfers, &transferv1.TransferInfo{
			TransferId:            t.TransferID,
			SenderNodeId:          t.SenderNodeID,
			ReceiverNodeId:        t.ReceiverNodeID,
			Filename:              t.Filename,
			TotalSizeBytes:        t.TotalSizeBytes,
			Status:                transferv1.TransferStatus(t.Status),
			CreatedAt:             timestamppb.New(t.CreatedAt),
			SenderEphemeralPubkey: t.SenderEphemeralPubkey,
			ContentHash:           t.ContentHash,
			GroupTransferId:       groupID,
			WrappedFileKey:        t.WrappedFileKey,
		})
	}

	log.Info().
		Str("group_transfer_id", groupID).
		Str("session_id", req.SessionId).
		Int("recipients", len(transfers)).
		Msg("group transfer initiated")

	return &transferv1.InitiateGroupTransferResponse{
		GroupTransferId: gt.ID,
		TotalChunks:     totalChunks,
		Transfers:       transfers,
		CreatedAt:       timestamppb.New(gt.CreatedAt),
	}, nil
}

func (h *TransferHandler) ListGroupTransfers(ctx context.Context, req *transferv1.ListGroupTransfersRequest) (*transferv1.ListGroupTransfersResponse, error) {
	if req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}

	sessionUUID, err := uuid.Parse(req.SessionId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid session_id format")
	}

	groups, err := h.queries.ListGroupTransfersBySession(ctx, pgUUID(sessionUUID))
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list group transfers")
	}

	result := make([]*transferv1.GroupTransferInfo, 0, len(groups))
	for _, g := range groups {
		childRows, err := h.queries.ListTransfersByGroupID(ctx, pgText(g.ID))
		if err != nil {
			continue
		}

		children := make([]*transferv1.TransferInfo, 0, len(childRows))
		for _, t := range childRows {
			progress := int32(0)
			if t.TotalChunks > 0 {
				progress = (t.ChunksDone * 100) / t.TotalChunks
			}
			children = append(children, &transferv1.TransferInfo{
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
				GroupTransferId:       g.ID,
				WrappedFileKey:        t.WrappedFileKey,
			})
		}

		result = append(result, &transferv1.GroupTransferInfo{
			GroupTransferId: g.ID,
			SessionId:       req.SessionId,
			SenderNodeId:    g.SenderNodeID,
			Filename:        g.Filename,
			TotalSizeBytes:  g.TotalSizeBytes,
			TotalChunks:     g.TotalChunks,
			Transfers:       children,
			CreatedAt:       timestamppb.New(g.CreatedAt),
		})
	}

	return &transferv1.ListGroupTransfersResponse{GroupTransfers: result}, nil
}

func (h *TransferHandler) AdminListTransfers(ctx context.Context, req *transferv1.AdminListTransfersRequest) (*transferv1.AdminListTransfersResponse, error) {
	limit := req.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := h.queries.AdminListTransfers(ctx, repository.AdminListTransfersParams{
		Limit: limit, Offset: req.Offset,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list transfers")
	}
	total, _ := h.queries.CountTransfers(ctx)
	transfers := make([]*transferv1.AdminTransfer, len(rows))
	for i, t := range rows {
		transfers[i] = &transferv1.AdminTransfer{
			TransferId:     t.TransferID,
			SenderNodeId:   t.SenderNodeID,
			ReceiverNodeId: t.ReceiverNodeID,
			Filename:       t.Filename,
			TotalSizeBytes: t.TotalSizeBytes,
			Status:         t.Status,
			ChunksDone:     t.ChunksDone,
			TotalChunks:    t.TotalChunks,
			ContentHash:    t.ContentHash,
			TransferMode:   t.TransferMode,
			CreatedAt:      timestamppb.New(t.CreatedAt),
			UpdatedAt:      timestamppb.New(t.UpdatedAt),
		}
	}
	return &transferv1.AdminListTransfersResponse{Transfers: transfers, Total: total}, nil
}

func (h *TransferHandler) GetTransferStats(ctx context.Context, _ *transferv1.GetTransferStatsRequest) (*transferv1.GetTransferStatsResponse, error) {
	count, err := h.queries.CountTransfers(ctx)
	if err != nil {
		count = 0
	}
	bytes, err := h.queries.SumTransferredBytes(ctx)
	if err != nil {
		bytes = 0
	}
	return &transferv1.GetTransferStatsResponse{
		TotalTransfers: count,
		TotalBytes:     bytes,
	}, nil
}

func (h *TransferHandler) GetTransferActivity(ctx context.Context, req *transferv1.GetTransferActivityRequest) (*transferv1.GetTransferActivityResponse, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	rows, err := h.queries.GetTransferActivityHeatmap(ctx, req.NodeId)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get activity")
	}
	days := make([]*transferv1.DailyActivity, 0, len(rows))
	for _, r := range rows {
		days = append(days, &transferv1.DailyActivity{
			Date:          r.Day.Time.Format("2006-01-02"),
			TransferCount: r.TransferCount,
		})
	}
	return &transferv1.GetTransferActivityResponse{Days: days}, nil
}

func (h *TransferHandler) GetTransferSpeed(ctx context.Context, req *transferv1.GetTransferSpeedRequest) (*transferv1.GetTransferSpeedResponse, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	row, err := h.queries.GetTransferSpeedStats(ctx, req.NodeId)
	if err != nil {
		return &transferv1.GetTransferSpeedResponse{}, nil
	}
	// bytes_last_minute / 60 = bytes_per_sec
	bps := int64(0)
	if row.BytesLastMinute > 0 {
		bps = row.BytesLastMinute / 60
	}
	return &transferv1.GetTransferSpeedResponse{
		BytesPerSec:     bps,
		ActiveTransfers: row.TransfersLastMinute,
	}, nil
}

// pgUUID converts a google uuid to pgtype.UUID.
func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// pgText converts a string to pgtype.Text.
func pgText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
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
