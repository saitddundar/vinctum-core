package relay

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	discoveryv1 "github.com/saitddundar/vinctum-core/proto/discovery/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type PeerPool struct {
	mu              sync.RWMutex
	conns           map[string]*grpc.ClientConn
	breakers        map[string]*CircuitBreaker
	discoveryClient discoveryv1.DiscoveryServiceClient
	maxFailures     int
	cooldown        time.Duration
}

func NewPeerPool(discoveryClient discoveryv1.DiscoveryServiceClient, maxFailures int, cooldown time.Duration) *PeerPool {
	return &PeerPool{
		conns:           make(map[string]*grpc.ClientConn),
		breakers:        make(map[string]*CircuitBreaker),
		discoveryClient: discoveryClient,
		maxFailures:     maxFailures,
		cooldown:        cooldown,
	}
}

func (p *PeerPool) GetConn(ctx context.Context, nodeID string) (*grpc.ClientConn, error) {
	p.mu.RLock()
	conn, ok := p.conns[nodeID]
	p.mu.RUnlock()
	if ok {
		return conn, nil
	}

	addr, err := p.resolveAddr(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("resolve address for %s: %w", nodeID, err)
	}

	conn, err = grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial %s at %s: %w", nodeID, addr, err)
	}

	p.mu.Lock()
	// Double-check another goroutine didn't create it already.
	if existing, ok := p.conns[nodeID]; ok {
		p.mu.Unlock()
		conn.Close()
		return existing, nil
	}
	p.conns[nodeID] = conn
	p.mu.Unlock()

	log.Debug().Str("node_id", nodeID).Str("addr", addr).Msg("new peer connection")
	return conn, nil
}

func (p *PeerPool) GetBreaker(nodeID string) *CircuitBreaker {
	p.mu.RLock()
	cb, ok := p.breakers[nodeID]
	p.mu.RUnlock()
	if ok {
		return cb
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if cb, ok := p.breakers[nodeID]; ok {
		return cb
	}
	cb = NewCircuitBreaker(p.maxFailures, p.cooldown)
	p.breakers[nodeID] = cb
	return cb
}

func (p *PeerPool) Evict(nodeID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if conn, ok := p.conns[nodeID]; ok {
		conn.Close()
		delete(p.conns, nodeID)
	}
	delete(p.breakers, nodeID)
}

func (p *PeerPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, conn := range p.conns {
		conn.Close()
		delete(p.conns, id)
	}
}

func (p *PeerPool) resolveAddr(ctx context.Context, nodeID string) (string, error) {
	resp, err := p.discoveryClient.GetNodeInfo(ctx, &discoveryv1.GetNodeInfoRequest{
		NodeId: nodeID,
	})
	if err != nil {
		return "", fmt.Errorf("discovery GetNodeInfo: %w", err)
	}
	if resp.Peer == nil || len(resp.Peer.Addrs) == 0 {
		return "", fmt.Errorf("no addresses found for node %s", nodeID)
	}
	// Use the first address as the gRPC target.
	return resp.Peer.Addrs[0], nil
}
