package handler

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	routingv1 "github.com/saitddundar/vinctum-core/proto/routing/v1"
	"github.com/saitddundar/vinctum-core/services/routing/repository"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type NodeIntelligence interface {
	ScoreNode(nodeID string) (score float64, ok bool)
	IsAnomalous(nodeID string) bool
}

type RoutingHandler struct {
	routingv1.UnimplementedRoutingServiceServer
	queries repository.Querier
	intel   NodeIntelligence
}

func NewRoutingHandler(q repository.Querier) *RoutingHandler {
	return &RoutingHandler{queries: q}
}

func (h *RoutingHandler) SetIntelligence(ni NodeIntelligence) {
	h.intel = ni
}

func (h *RoutingHandler) FindRoute(ctx context.Context, req *routingv1.FindRouteRequest) (*routingv1.FindRouteResponse, error) {
	if req.SourceNodeId == "" || req.TargetNodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "source_node_id and target_node_id are required")
	}

	maxHops := int(req.MaxHops)
	if maxHops <= 0 {
		maxHops = 5
	}

	// Look up the direct entry first.
	direct, err := h.queries.FindRoute(ctx, repository.FindRouteParams{
		NodeID:       req.SourceNodeId,
		TargetNodeID: req.TargetNodeId,
	})

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.Internal, "failed to query route table")
	}

	directPossible := err == nil && direct.NextHopID == req.TargetNodeId

	// Build exclusion set for rerouting around failed nodes.
	excluded := make(map[string]bool, len(req.ExcludeNodeIds))
	for _, id := range req.ExcludeNodeIds {
		excluded[id] = true
	}

	// Auto-exclude anomalous nodes detected by intelligence.
	if h.intel != nil {
		allRoutes, _ := h.queries.GetRoutesByNodeID(ctx, req.SourceNodeId)
		for _, r := range allRoutes {
			if excluded[r.NextHopID] {
				continue
			}
			if h.intel.IsAnomalous(r.NextHopID) {
				excluded[r.NextHopID] = true
				log.Warn().Str("node", r.NextHopID).Msg("auto-excluded anomalous node from route")
			}
		}
	}

	// Walk the hop chain.
	var hops []*routingv1.RouteHop
	var totalLatency int64
	current := req.SourceNodeId

	for i := 0; i < maxHops; i++ {
		entry, err := h.queries.FindRoute(ctx, repository.FindRouteParams{
			NodeID:       current,
			TargetNodeID: req.TargetNodeId,
		})
		if err != nil {
			break
		}

		// Skip excluded nodes (failed or anomalous).
		if excluded[entry.NextHopID] {
			log.Debug().Str("skipped", entry.NextHopID).Msg("excluded node from route")
			break
		}

		// Log intelligence score for observability.
		if h.intel != nil {
			if score, ok := h.intel.ScoreNode(entry.NextHopID); ok {
				log.Debug().Str("node", entry.NextHopID).Float64("score", score).Msg("hop node score")
			}
		}

		hops = append(hops, &routingv1.RouteHop{
			NodeId:   entry.NextHopID,
			HopIndex: int32(i + 1),
			IsRelay:  entry.NextHopID != req.TargetNodeId,
		})
		totalLatency += entry.LatencyMs

		if entry.NextHopID == req.TargetNodeId {
			break
		}
		current = entry.NextHopID
	}

	log.Info().
		Str("source", req.SourceNodeId).
		Str("target", req.TargetNodeId).
		Int("hops", len(hops)).
		Msg("route computed")

	return &routingv1.FindRouteResponse{
		Hops:               hops,
		TotalHops:          int32(len(hops)),
		EstimatedLatencyMs: totalLatency,
		DirectPossible:     directPossible,
	}, nil
}

// UpdateRouteTable upserts one or more routing entries for the requesting node.
func (h *RoutingHandler) UpdateRouteTable(ctx context.Context, req *routingv1.UpdateRouteTableRequest) (*routingv1.UpdateRouteTableResponse, error) {
	if req.NodeId == "" || len(req.Entries) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node_id and at least one entry are required")
	}

	var updated int32
	for _, e := range req.Entries {
		err := h.queries.UpsertRoute(ctx, repository.UpsertRouteParams{
			NodeID:       req.NodeId,
			TargetNodeID: e.TargetNodeId,
			NextHopID:    e.NextHopId,
			Metric:       e.Metric,
			LatencyMs:    e.LatencyMs,
		})
		if err != nil {
			log.Warn().Err(err).Str("target", e.TargetNodeId).Msg("failed to upsert route entry")
			continue
		}
		updated++
	}

	log.Info().Str("node_id", req.NodeId).Int32("entries_updated", updated).Msg("route table updated")

	return &routingv1.UpdateRouteTableResponse{
		Success:        updated > 0,
		EntriesUpdated: updated,
		UpdatedAt:      timestamppb.Now(),
	}, nil
}

func (h *RoutingHandler) GetRouteTable(ctx context.Context, req *routingv1.GetRouteTableRequest) (*routingv1.GetRouteTableResponse, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}

	rows, err := h.queries.GetRoutesByNodeID(ctx, req.NodeId)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to fetch route table")
	}

	entries := make([]*routingv1.RouteEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, &routingv1.RouteEntry{
			TargetNodeId: r.TargetNodeID,
			NextHopId:    r.NextHopID,
			Metric:       r.Metric,
			LatencyMs:    r.LatencyMs,
		})
	}

	return &routingv1.GetRouteTableResponse{
		NodeId:  req.NodeId,
		Entries: entries,
	}, nil
}

func (h *RoutingHandler) ListRelays(ctx context.Context, req *routingv1.ListRelaysRequest) (*routingv1.ListRelaysResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}

	rows, err := h.queries.ListRelays(ctx, limit)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list relays")
	}

	relays := make([]*routingv1.RelayInfo, 0, len(rows))
	for _, r := range rows {
		relays = append(relays, &routingv1.RelayInfo{
			NodeId:         r.NodeID,
			Address:        r.Address,
			ActiveCircuits: r.ActiveCircuits,
			MaxCircuits:    r.MaxCircuits,
			LatencyMs:      r.LatencyMs,
			LastSeen:       timestamppb.New(r.LastSeen),
		})
	}

	return &routingv1.ListRelaysResponse{Relays: relays}, nil
}

func (h *RoutingHandler) RegisterRelay(ctx context.Context, req *routingv1.RegisterRelayRequest) (*routingv1.RegisterRelayResponse, error) {
	if req.NodeId == "" || req.Address == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id and address are required")
	}

	maxCircuits := req.MaxCircuits
	if maxCircuits <= 0 {
		maxCircuits = 64
	}

	err := h.queries.UpsertRelay(ctx, repository.UpsertRelayParams{
		NodeID:         req.NodeId,
		Address:        req.Address,
		MaxCircuits:    maxCircuits,
		ActiveCircuits: 0,
		LatencyMs:      0,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to register relay")
	}

	log.Info().Str("node_id", req.NodeId).Str("address", req.Address).Msg("relay registered")

	return &routingv1.RegisterRelayResponse{
		Success:      true,
		RegisteredAt: timestamppb.Now(),
	}, nil
}
