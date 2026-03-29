package relay

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	routingv1 "github.com/saitddundar/vinctum-core/proto/routing/v1"
)

// Rerouter handles finding alternative routes when a hop fails.
type Rerouter struct {
	routingClient routingv1.RoutingServiceClient
}

func NewRerouter(rc routingv1.RoutingServiceClient) *Rerouter {
	return &Rerouter{routingClient: rc}
}

// FindAlternativeRoute computes a new route excluding the failed node.
func (r *Rerouter) FindAlternativeRoute(ctx context.Context, sourceNodeID, targetNodeID, failedNodeID string) ([]*routingv1.RouteHop, error) {
	if r.routingClient == nil {
		return nil, fmt.Errorf("routing client not available")
	}

	resp, err := r.routingClient.FindRoute(ctx, &routingv1.FindRouteRequest{
		SourceNodeId:   sourceNodeID,
		TargetNodeId:   targetNodeID,
		MaxHops:        10,
		ExcludeNodeIds: []string{failedNodeID},
	})
	if err != nil {
		return nil, fmt.Errorf("reroute failed: %w", err)
	}

	if len(resp.Hops) == 0 {
		return nil, fmt.Errorf("no alternative route found excluding node %s", failedNodeID)
	}

	log.Info().
		Str("source", sourceNodeID).
		Str("target", targetNodeID).
		Str("excluded", failedNodeID).
		Int("new_hops", len(resp.Hops)).
		Msg("alternative route found")

	return resp.Hops, nil
}
