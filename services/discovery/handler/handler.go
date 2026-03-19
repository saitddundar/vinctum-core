package handler

import (
	"context"

	"github.com/rs/zerolog/log"
	discoveryv1 "github.com/saitddundar/vinctum-core/proto/discovery/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type DiscoveryHandler struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func NewDiscoveryHandler() *DiscoveryHandler {
	return &DiscoveryHandler{}
}

func (h *DiscoveryHandler) AnnounceNode(ctx context.Context, req *discoveryv1.AnnounceNodeRequest) (*discoveryv1.AnnounceNodeResponse, error) {
	log.Info().Str("node_id", req.NodeId).Msg("node announce")

	if req.NodeId == "" || len(req.Addrs) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node_id and addrs are required")
	}

	// TODO: store in DHT / peer store
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

func (h *DiscoveryHandler) FindPeers(ctx context.Context, req *discoveryv1.FindPeersRequest) (*discoveryv1.FindPeersResponse, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}

	// TODO: query DHT for closest peers
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

func (h *DiscoveryHandler) GetNodeInfo(ctx context.Context, req *discoveryv1.GetNodeInfoRequest) (*discoveryv1.GetNodeInfoResponse, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}

	// TODO: lookup peer in store
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

func (h *DiscoveryHandler) StreamPeerUpdates(req *discoveryv1.StreamPeerUpdatesRequest, stream discoveryv1.DiscoveryService_StreamPeerUpdatesServer) error {
	log.Info().Str("node_id", req.NodeId).Msg("peer updates stream opened")

	// TODO: subscribe to peer events and stream them
	return status.Error(codes.Unimplemented, "not implemented yet")
}
