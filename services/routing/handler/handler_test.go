package handler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/saitddundar/vinctum-core/services/routing/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	routingv1 "github.com/saitddundar/vinctum-core/proto/routing/v1"
	"github.com/saitddundar/vinctum-core/services/routing/handler"
)

// ──────────────────── fake querier ────────────────────────

type fakeQuerier struct {
	routes map[string][]repository.Route // keyed by node_id
	relays map[string]repository.Relay   // keyed by node_id
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{
		routes: make(map[string][]repository.Route),
		relays: make(map[string]repository.Relay),
	}
}

var errNotFound = errors.New("not found")

func (f *fakeQuerier) UpsertRoute(_ context.Context, arg repository.UpsertRouteParams) error {
	routes := f.routes[arg.NodeID]
	for i, r := range routes {
		if r.TargetNodeID == arg.TargetNodeID {
			routes[i].NextHopID = arg.NextHopID
			routes[i].Metric = arg.Metric
			routes[i].LatencyMs = arg.LatencyMs
			routes[i].UpdatedAt = time.Now()
			f.routes[arg.NodeID] = routes
			return nil
		}
	}
	f.routes[arg.NodeID] = append(routes, repository.Route{
		NodeID:       arg.NodeID,
		TargetNodeID: arg.TargetNodeID,
		NextHopID:    arg.NextHopID,
		Metric:       arg.Metric,
		LatencyMs:    arg.LatencyMs,
		UpdatedAt:    time.Now(),
	})
	return nil
}

func (f *fakeQuerier) GetRoutesByNodeID(_ context.Context, nodeID string) ([]repository.Route, error) {
	return f.routes[nodeID], nil
}

func (f *fakeQuerier) FindRoute(_ context.Context, arg repository.FindRouteParams) (repository.Route, error) {
	for _, r := range f.routes[arg.NodeID] {
		if r.TargetNodeID == arg.TargetNodeID {
			return r, nil
		}
	}
	return repository.Route{}, errNotFound
}

func (f *fakeQuerier) DeleteRoute(_ context.Context, arg repository.DeleteRouteParams) error {
	routes := f.routes[arg.NodeID]
	for i, r := range routes {
		if r.TargetNodeID == arg.TargetNodeID {
			f.routes[arg.NodeID] = append(routes[:i], routes[i+1:]...)
			return nil
		}
	}
	return nil
}

func (f *fakeQuerier) UpsertRelay(_ context.Context, arg repository.UpsertRelayParams) error {
	f.relays[arg.NodeID] = repository.Relay{
		NodeID:         arg.NodeID,
		Address:        arg.Address,
		MaxCircuits:    arg.MaxCircuits,
		ActiveCircuits: arg.ActiveCircuits,
		LatencyMs:      arg.LatencyMs,
		LastSeen:       time.Now(),
	}
	return nil
}

func (f *fakeQuerier) ListRelays(_ context.Context, limit int32) ([]repository.Relay, error) {
	var result []repository.Relay
	for _, r := range f.relays {
		result = append(result, r)
		if int32(len(result)) >= limit {
			break
		}
	}
	return result, nil
}

func (f *fakeQuerier) GetRelay(_ context.Context, nodeID string) (repository.Relay, error) {
	r, ok := f.relays[nodeID]
	if !ok {
		return repository.Relay{}, errNotFound
	}
	return r, nil
}

// Compile-time interface check.
var _ repository.Querier = (*fakeQuerier)(nil)

// ──────────────────── tests ──────────────────────────────

func newTestHandler() *handler.RoutingHandler {
	return handler.NewRoutingHandler(newFakeQuerier())
}

func TestUpdateRouteTable(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		resp, err := h.UpdateRouteTable(ctx, &routingv1.UpdateRouteTableRequest{
			NodeId: "node-A",
			Entries: []*routingv1.RouteEntry{
				{TargetNodeId: "node-B", NextHopId: "node-B", Metric: 1, LatencyMs: 10},
				{TargetNodeId: "node-C", NextHopId: "node-B", Metric: 2, LatencyMs: 25},
			},
		})
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, int32(2), resp.EntriesUpdated)
	})

	t.Run("missing fields", func(t *testing.T) {
		_, err := h.UpdateRouteTable(ctx, &routingv1.UpdateRouteTableRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestGetRouteTable(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	// Seed a route.
	_, _ = h.UpdateRouteTable(ctx, &routingv1.UpdateRouteTableRequest{
		NodeId: "node-A",
		Entries: []*routingv1.RouteEntry{
			{TargetNodeId: "node-B", NextHopId: "node-B", Metric: 1, LatencyMs: 10},
		},
	})

	t.Run("success", func(t *testing.T) {
		resp, err := h.GetRouteTable(ctx, &routingv1.GetRouteTableRequest{NodeId: "node-A"})
		require.NoError(t, err)
		assert.Len(t, resp.Entries, 1)
		assert.Equal(t, "node-B", resp.Entries[0].TargetNodeId)
	})

	t.Run("empty table", func(t *testing.T) {
		resp, err := h.GetRouteTable(ctx, &routingv1.GetRouteTableRequest{NodeId: "node-Z"})
		require.NoError(t, err)
		assert.Empty(t, resp.Entries)
	})

	t.Run("missing node_id", func(t *testing.T) {
		_, err := h.GetRouteTable(ctx, &routingv1.GetRouteTableRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestFindRoute(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	// Build route chain: A→B→C (A's next-hop to C goes through B).
	_, _ = h.UpdateRouteTable(ctx, &routingv1.UpdateRouteTableRequest{
		NodeId: "node-A",
		Entries: []*routingv1.RouteEntry{
			{TargetNodeId: "node-C", NextHopId: "node-B", Metric: 2, LatencyMs: 15},
		},
	})
	_, _ = h.UpdateRouteTable(ctx, &routingv1.UpdateRouteTableRequest{
		NodeId: "node-B",
		Entries: []*routingv1.RouteEntry{
			{TargetNodeId: "node-C", NextHopId: "node-C", Metric: 1, LatencyMs: 10},
		},
	})

	t.Run("multi-hop route", func(t *testing.T) {
		resp, err := h.FindRoute(ctx, &routingv1.FindRouteRequest{
			SourceNodeId: "node-A",
			TargetNodeId: "node-C",
		})
		require.NoError(t, err)
		assert.Equal(t, int32(2), resp.TotalHops)
		assert.False(t, resp.DirectPossible)
	})

	t.Run("direct route", func(t *testing.T) {
		_, _ = h.UpdateRouteTable(ctx, &routingv1.UpdateRouteTableRequest{
			NodeId: "node-X",
			Entries: []*routingv1.RouteEntry{
				{TargetNodeId: "node-Y", NextHopId: "node-Y", Metric: 1, LatencyMs: 5},
			},
		})
		resp, err := h.FindRoute(ctx, &routingv1.FindRouteRequest{
			SourceNodeId: "node-X",
			TargetNodeId: "node-Y",
		})
		require.NoError(t, err)
		assert.True(t, resp.DirectPossible)
		assert.Equal(t, int32(1), resp.TotalHops)
	})

	t.Run("missing fields", func(t *testing.T) {
		_, err := h.FindRoute(ctx, &routingv1.FindRouteRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestRegisterRelay(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		resp, err := h.RegisterRelay(ctx, &routingv1.RegisterRelayRequest{
			NodeId:      "relay-1",
			Address:     "/ip4/1.2.3.4/tcp/4001",
			MaxCircuits: 128,
		})
		require.NoError(t, err)
		assert.True(t, resp.Success)
	})

	t.Run("missing fields", func(t *testing.T) {
		_, err := h.RegisterRelay(ctx, &routingv1.RegisterRelayRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestListRelays(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	// Register two relays.
	_, _ = h.RegisterRelay(ctx, &routingv1.RegisterRelayRequest{
		NodeId: "relay-1", Address: "/ip4/1.2.3.4/tcp/4001", MaxCircuits: 64,
	})
	_, _ = h.RegisterRelay(ctx, &routingv1.RegisterRelayRequest{
		NodeId: "relay-2", Address: "/ip4/5.6.7.8/tcp/4001", MaxCircuits: 32,
	})

	t.Run("list all", func(t *testing.T) {
		resp, err := h.ListRelays(ctx, &routingv1.ListRelaysRequest{Limit: 10})
		require.NoError(t, err)
		assert.Len(t, resp.Relays, 2)
	})

	t.Run("limit", func(t *testing.T) {
		resp, err := h.ListRelays(ctx, &routingv1.ListRelaysRequest{Limit: 1})
		require.NoError(t, err)
		assert.Len(t, resp.Relays, 1)
	})
}
