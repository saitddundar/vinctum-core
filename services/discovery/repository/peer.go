package repository

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Peer struct {
	NodeID    string
	Addrs     []string
	PublicKey string
	IsRelay   bool
	LastSeen  time.Time
}

type PeerRepository interface {
	Upsert(ctx context.Context, p *Peer) error
	Find(ctx context.Context, nodeID string) (*Peer, error)
	All(ctx context.Context) ([]*Peer, error)
}

type InMemoryPeerRepository struct {
	mu    sync.RWMutex
	peers map[string]*Peer
}

func NewInMemoryPeerRepository() *InMemoryPeerRepository {
	return &InMemoryPeerRepository{peers: make(map[string]*Peer)}
}

func (r *InMemoryPeerRepository) Upsert(ctx context.Context, p *Peer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.LastSeen = time.Now()
	r.peers[p.NodeID] = p
	return nil
}

func (r *InMemoryPeerRepository) Find(ctx context.Context, nodeID string) (*Peer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.peers[nodeID]
	if !ok {
		return nil, fmt.Errorf("peer not found: %s", nodeID)
	}
	return p, nil
}

func (r *InMemoryPeerRepository) All(ctx context.Context) ([]*Peer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	peers := make([]*Peer, 0, len(r.peers))
	for _, p := range r.peers {
		peers = append(peers, p)
	}
	return peers, nil
}
