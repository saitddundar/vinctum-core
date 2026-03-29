package handler_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	discoveryv1 "github.com/saitddundar/vinctum-core/proto/discovery/v1"
	"github.com/saitddundar/vinctum-core/services/discovery/handler"
	"github.com/saitddundar/vinctum-core/services/discovery/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ──────────────────── fake querier ────────────────────────

type fakeQuerier struct {
	peers map[string]repository.Peer
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{peers: make(map[string]repository.Peer)}
}

func (f *fakeQuerier) UpsertPeer(_ context.Context, arg repository.UpsertPeerParams) error {
	f.peers[arg.NodeID] = repository.Peer{
		NodeID:    arg.NodeID,
		Addrs:     arg.Addrs,
		PublicKey: arg.PublicKey,
		IsRelay:   arg.IsRelay,
		LastSeen:  time.Now(),
	}
	return nil
}

func (f *fakeQuerier) GetPeer(_ context.Context, nodeID string) (repository.Peer, error) {
	p, ok := f.peers[nodeID]
	if !ok {
		return repository.Peer{}, pgx.ErrNoRows
	}
	return p, nil
}

func (f *fakeQuerier) ListPeers(_ context.Context) ([]repository.Peer, error) {
	var result []repository.Peer
	for _, p := range f.peers {
		result = append(result, p)
	}
	return result, nil
}

var _ repository.Querier = (*fakeQuerier)(nil)

// ──────────────────── fake P2P node ─────────────────────

type fakeP2PNode struct {
	addrs []string
}

func (f *fakeP2PNode) Addresses() []string { return f.addrs }

// ──────────────────── tests ──────────────────────────────

func newTestHandler() *handler.DiscoveryHandler {
	return handler.NewDiscoveryHandler(newFakeQuerier(), nil)
}

func newTestHandlerWithP2P() *handler.DiscoveryHandler {
	p2p := &fakeP2PNode{addrs: []string{"/ip4/127.0.0.1/tcp/4001"}}
	return handler.NewDiscoveryHandler(newFakeQuerier(), p2p)
}

func TestAnnounceNode(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		resp, err := h.AnnounceNode(ctx, &discoveryv1.AnnounceNodeRequest{
			NodeId:    "node-A",
			Addrs:     []string{"/ip4/1.2.3.4/tcp/4001"},
			PublicKey: "pk-A",
		})
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.NotNil(t, resp.AnnouncedAt)
	})

	t.Run("with p2p node", func(t *testing.T) {
		hp := newTestHandlerWithP2P()
		resp, err := hp.AnnounceNode(ctx, &discoveryv1.AnnounceNodeRequest{
			NodeId: "node-B",
			Addrs:  []string{"/ip4/5.6.7.8/tcp/4001"},
		})
		require.NoError(t, err)
		assert.True(t, resp.Success)
	})

	t.Run("missing node_id", func(t *testing.T) {
		_, err := h.AnnounceNode(ctx, &discoveryv1.AnnounceNodeRequest{
			Addrs: []string{"/ip4/1.2.3.4/tcp/4001"},
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("missing addrs", func(t *testing.T) {
		_, err := h.AnnounceNode(ctx, &discoveryv1.AnnounceNodeRequest{
			NodeId: "node-A",
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestFindPeers(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	// Seed peers.
	_, _ = h.AnnounceNode(ctx, &discoveryv1.AnnounceNodeRequest{
		NodeId: "node-A", Addrs: []string{"/ip4/1.1.1.1/tcp/4001"},
	})
	_, _ = h.AnnounceNode(ctx, &discoveryv1.AnnounceNodeRequest{
		NodeId: "node-B", Addrs: []string{"/ip4/2.2.2.2/tcp/4001"},
	})
	_, _ = h.AnnounceNode(ctx, &discoveryv1.AnnounceNodeRequest{
		NodeId: "node-C", Addrs: []string{"/ip4/3.3.3.3/tcp/4001"},
	})

	t.Run("excludes self", func(t *testing.T) {
		resp, err := h.FindPeers(ctx, &discoveryv1.FindPeersRequest{
			NodeId: "node-A", Count: 10,
		})
		require.NoError(t, err)
		for _, p := range resp.Peers {
			assert.NotEqual(t, "node-A", p.NodeId)
		}
		assert.Len(t, resp.Peers, 2)
	})

	t.Run("respects count limit", func(t *testing.T) {
		resp, err := h.FindPeers(ctx, &discoveryv1.FindPeersRequest{
			NodeId: "node-A", Count: 1,
		})
		require.NoError(t, err)
		assert.Len(t, resp.Peers, 1)
	})

	t.Run("missing node_id", func(t *testing.T) {
		_, err := h.FindPeers(ctx, &discoveryv1.FindPeersRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestGetNodeInfo(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	_, _ = h.AnnounceNode(ctx, &discoveryv1.AnnounceNodeRequest{
		NodeId: "node-A", Addrs: []string{"/ip4/1.1.1.1/tcp/4001"}, PublicKey: "pk-A",
	})

	t.Run("success", func(t *testing.T) {
		resp, err := h.GetNodeInfo(ctx, &discoveryv1.GetNodeInfoRequest{NodeId: "node-A"})
		require.NoError(t, err)
		assert.Equal(t, "node-A", resp.Peer.NodeId)
		assert.Equal(t, "pk-A", resp.Peer.PublicKey)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := h.GetNodeInfo(ctx, &discoveryv1.GetNodeInfoRequest{NodeId: "nonexistent"})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("missing node_id", func(t *testing.T) {
		_, err := h.GetNodeInfo(ctx, &discoveryv1.GetNodeInfoRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}
