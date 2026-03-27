package handler

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	discoveryv1 "github.com/saitddundar/vinctum-core/proto/discovery/v1"
	"github.com/saitddundar/vinctum-core/services/discovery/repository"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type DiscoveryHandler struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	queries repository.Querier
}

func NewDiscoveryHandler(q repository.Querier) *DiscoveryHandler {
	return &DiscoveryHandler{queries: q}
}

func (h *DiscoveryHandler) AnnounceNode(ctx context.Context, req *discoveryv1.AnnounceNodeRequest) (*discoveryv1.AnnounceNodeResponse, error) {
	if req.NodeId == "" || len(req.Addrs) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node_id and addrs are required")
	}

	err := h.queries.UpsertPeer(ctx, repository.UpsertPeerParams{
		NodeID:    req.NodeId,
		Addrs:     req.Addrs,
		PublicKey: req.PublicKey,
		IsRelay:   false,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to store peer")
	}

	log.Info().Str("node_id", req.NodeId).Strs("addrs", req.Addrs).Msg("node announced")

	return &discoveryv1.AnnounceNodeResponse{
		Success:     true,
		AnnouncedAt: timestamppb.Now(),
	}, nil
}

func (h *DiscoveryHandler) FindPeers(ctx context.Context, req *discoveryv1.FindPeersRequest) (*discoveryv1.FindPeersResponse, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}

	all, err := h.queries.ListPeers(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to fetch peers")
	}

	limit := int(req.Count)
	if limit <= 0 || limit > len(all) {
		limit = len(all)
	}

	result := make([]*discoveryv1.PeerInfo, 0, limit)
	for _, p := range all {
		if p.NodeID == req.NodeId {
			continue
		}
		result = append(result, toPeerInfo(p))
		if len(result) >= limit {
			break
		}
	}

	return &discoveryv1.FindPeersResponse{Peers: result}, nil
}

func (h *DiscoveryHandler) GetNodeInfo(ctx context.Context, req *discoveryv1.GetNodeInfoRequest) (*discoveryv1.GetNodeInfoResponse, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}

	p, err := h.queries.GetPeer(ctx, req.NodeId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "peer not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch peer")
	}

	return &discoveryv1.GetNodeInfoResponse{Peer: toPeerInfo(p)}, nil
}

func (h *DiscoveryHandler) StreamPeerUpdates(req *discoveryv1.StreamPeerUpdatesRequest, stream discoveryv1.DiscoveryService_StreamPeerUpdatesServer) error {
	if req.NodeId == "" {
		return status.Error(codes.InvalidArgument, "node_id is required")
	}

	// Track known peers to detect joins / leaves / updates.
	known := make(map[string]repository.Peer)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
			all, err := h.queries.ListPeers(stream.Context())
			if err != nil {
				continue
			}
			current := make(map[string]repository.Peer, len(all))
			for _, p := range all {
				if p.NodeID == req.NodeId {
					continue // skip self
				}
				current[p.NodeID] = p

				prev, exists := known[p.NodeID]
				var updateType discoveryv1.PeerUpdate_UpdateType
				if !exists {
					updateType = discoveryv1.PeerUpdate_PEER_JOINED
				} else if !peerEqual(prev, p) {
					updateType = discoveryv1.PeerUpdate_PEER_UPDATED
				} else {
					continue // no change
				}

				if err := stream.Send(&discoveryv1.PeerUpdate{
					Type:      updateType,
					Peer:      toPeerInfo(p),
					Timestamp: timestamppb.Now(),
				}); err != nil {
					return err
				}
			}

			// Detect peers that left.
			for id, p := range known {
				if _, ok := current[id]; !ok {
					_ = stream.Send(&discoveryv1.PeerUpdate{
						Type:      discoveryv1.PeerUpdate_PEER_LEFT,
						Peer:      toPeerInfo(p),
						Timestamp: timestamppb.Now(),
					})
				}
			}

			known = current
		}
	}
}

func peerEqual(a, b repository.Peer) bool {
	if len(a.Addrs) != len(b.Addrs) {
		return false
	}
	for i := range a.Addrs {
		if a.Addrs[i] != b.Addrs[i] {
			return false
		}
	}
	return a.PublicKey == b.PublicKey && a.IsRelay == b.IsRelay
}

func toPeerInfo(p repository.Peer) *discoveryv1.PeerInfo {
	return &discoveryv1.PeerInfo{
		NodeId:    p.NodeID,
		Addrs:     p.Addrs,
		PublicKey: p.PublicKey,
		IsRelay:   p.IsRelay,
		LastSeen:  timestamppb.New(p.LastSeen),
	}
}
