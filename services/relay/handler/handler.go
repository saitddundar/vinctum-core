package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saitddundar/vinctum-core/internal/relay"
	relayv1 "github.com/saitddundar/vinctum-core/proto/relay/v1"
	routingv1 "github.com/saitddundar/vinctum-core/proto/routing/v1"
	"github.com/saitddundar/vinctum-core/services/transfer/storage"
)

type RelayHandler struct {
	relayv1.UnimplementedRelayServiceServer

	nodeID      string
	chunks      storage.ChunkStore
	relayClient *relay.Client
	rerouter    *relay.Rerouter
}

func NewRelayHandler(nodeID string, chunks storage.ChunkStore, relayClient *relay.Client, rerouter *relay.Rerouter) *RelayHandler {
	return &RelayHandler{
		nodeID:      nodeID,
		chunks:      chunks,
		relayClient: relayClient,
		rerouter:    rerouter,
	}
}

func (h *RelayHandler) RelayChunk(ctx context.Context, req *relayv1.RelayChunkRequest) (*relayv1.RelayChunkResponse, error) {
	if req.TransferId == "" || req.Data == nil {
		return nil, status.Error(codes.InvalidArgument, "transfer_id and data are required")
	}

	// TTL check — drop if expired.
	if req.Ttl <= 0 {
		return nil, status.Error(codes.ResourceExhausted, "TTL expired, dropping chunk")
	}

	// Verify chunk hash integrity.
	if req.ChunkHash != "" {
		h := sha256.Sum256(req.Data)
		if hex.EncodeToString(h[:]) != req.ChunkHash {
			return nil, status.Error(codes.DataLoss, "chunk hash mismatch")
		}
	}

	// If no remaining hops or this node is the final destination, store locally.
	if len(req.RemainingHops) == 0 || h.isFinalDestination(req.RemainingHops) {
		_, err := h.chunks.SaveChunk(req.TransferId, req.ChunkIndex, req.Data)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "save chunk: %v", err)
		}
		log.Info().
			Str("transfer_id", req.TransferId).
			Int32("chunk_index", req.ChunkIndex).
			Msg("chunk stored at final destination")

		return &relayv1.RelayChunkResponse{
			Success: true,
			NodeId:  h.nodeID,
		}, nil
	}

	// Forward to next hop.
	nextHop := req.RemainingHops[0]
	forwardReq := &relayv1.RelayChunkRequest{
		TransferId:        req.TransferId,
		ChunkIndex:        req.ChunkIndex,
		Data:              req.Data,
		ChunkHash:         req.ChunkHash,
		RemainingHops:     req.RemainingHops[1:], // Pop the next hop
		ReplicationFactor: req.ReplicationFactor,
		Ttl:               req.Ttl - 1,
		EncryptionKey:     req.EncryptionKey,
	}

	resp, err := h.relayClient.ForwardChunk(ctx, nextHop.NodeId, forwardReq)
	if err != nil {
		log.Warn().
			Str("next_hop", nextHop.NodeId).
			Err(err).
			Msg("forward failed, attempting reroute")

		// Try rerouting around the failed node.
		if h.rerouter != nil && len(req.RemainingHops) > 0 {
			finalDest := req.RemainingHops[len(req.RemainingHops)-1].NodeId
			newHops, rerouteErr := h.rerouter.FindAlternativeRoute(ctx, h.nodeID, finalDest, nextHop.NodeId)
			if rerouteErr == nil && len(newHops) > 0 {
				log.Info().
					Str("failed_node", nextHop.NodeId).
					Int("new_hops", len(newHops)).
					Msg("reroute successful, forwarding via new path")

				forwardReq.RemainingHops = newHops[1:] // Skip self
				newNext := newHops[0].NodeId
				resp, err = h.relayClient.ForwardChunk(ctx, newNext, forwardReq)
				if err == nil {
					return resp, nil
				}
				log.Warn().Err(err).Str("new_next", newNext).Msg("rerouted forward also failed")
			}
		}

		// Final fallback: store locally so data isn't lost.
		_, saveErr := h.chunks.SaveChunk(req.TransferId, req.ChunkIndex, req.Data)
		if saveErr != nil {
			return nil, status.Errorf(codes.Internal, "forward and local save both failed: forward=%v, save=%v", err, saveErr)
		}

		return &relayv1.RelayChunkResponse{
			Success:      false,
			NodeId:       h.nodeID,
			ErrorMessage: "stored locally after forward failure: " + err.Error(),
		}, nil
	}

	return resp, nil
}

func (h *RelayHandler) FetchChunk(_ context.Context, req *relayv1.FetchChunkRequest) (*relayv1.FetchChunkResponse, error) {
	if req.TransferId == "" {
		return nil, status.Error(codes.InvalidArgument, "transfer_id is required")
	}

	data, err := h.chunks.LoadChunk(req.TransferId, req.ChunkIndex)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "chunk not found: %v", err)
	}

	hash := sha256.Sum256(data)
	return &relayv1.FetchChunkResponse{
		Data:      data,
		ChunkHash: hex.EncodeToString(hash[:]),
	}, nil
}

func (h *RelayHandler) Ping(_ context.Context, _ *relayv1.PingRequest) (*relayv1.PingResponse, error) {
	return &relayv1.PingResponse{
		NodeId:    h.nodeID,
		Timestamp: time.Now().UnixMilli(),
	}, nil
}

func (h *RelayHandler) isFinalDestination(hops []*routingv1.RouteHop) bool {
	if len(hops) == 0 {
		return true
	}
	// If the only remaining hop is us, we're the destination.
	return len(hops) == 1 && hops[0].NodeId == h.nodeID
}
