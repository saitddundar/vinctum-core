package handler

import (
	"context"

	"github.com/rs/zerolog/log"
	discoveryv1 "github.com/saitddundar/vinctum-core/proto/discovery/v1"
	"github.com/saitddundar/vinctum-core/services/discovery/repository"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type DiscoveryHandler struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	peers repository.PeerRepository
}

func NewDiscoveryHandler(peers repository.PeerRepository) *DiscoveryHandler {
	return &DiscoveryHandler{peers: peers}
}

func (h *DiscoveryHandler) AnnounceNode(ctx context.Context, req *discoveryv1.AnnounceNodeRequest) (*discoveryv1.AnnounceNodeResponse, error) {
	if req.NodeId == "" || len(req.Addrs) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node_id and addrs are required")
	}

	peer := &repository.Peer{
		NodeID:    req.NodeId,
		Addrs:     req.Addrs,
		PublicKey: req.PublicKey,
	}

	if err := h.peers.Upsert(ctx, peer); err != nil {
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

	all, err := h.peers.All(ctx)
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

	p, err := h.peers.Find(ctx, req.NodeId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "peer not found")
	}

	return &discoveryv1.GetNodeInfoResponse{Peer: toPeerInfo(p)}, nil
}

func (h *DiscoveryHandler) StreamPeerUpdates(req *discoveryv1.StreamPeerUpdatesRequest, stream discoveryv1.DiscoveryService_StreamPeerUpdatesServer) error {
	// Full pub/sub implementation coming in Week 3 (P2P layer).
	return status.Error(codes.Unimplemented, "streaming not yet available")
}

func toPeerInfo(p *repository.Peer) *discoveryv1.PeerInfo {
	return &discoveryv1.PeerInfo{
		NodeId:    p.NodeID,
		Addrs:     p.Addrs,
		PublicKey: p.PublicKey,
		IsRelay:   p.IsRelay,
		LastSeen:  timestamppb.New(p.LastSeen),
	}
}
