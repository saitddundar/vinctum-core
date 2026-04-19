package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/multiformats/go-multiaddr"
	"github.com/rs/zerolog/log"
)

// NodeConfig holds the configuration needed to start a P2P node.
type NodeConfig struct {
	ListenAddrs       []string
	BootstrapPeers    []string
	PrivateKey        []byte // Raw Ed25519 or RSA depending on crypto implementation
	EnableRelay       bool
	EnableDHT         bool
	EnableHolePunch   bool // Enable NAT hole punching for direct P2P
}

// Node wraps a libp2p host and its associated DHT instance.
type Node struct {
	Host host.Host
	DHT  *dht.IpfsDHT
}

// NewNode initialises and starts a libp2p node with Kademlia DHT, NAT port
// mapping, and optional relay support.
func NewNode(ctx context.Context, cfg NodeConfig) (*Node, error) {
	var kadDHT *dht.IpfsDHT

	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(cfg.ListenAddrs...),
		libp2p.NATPortMap(), // auto-map NAT ports if possible
		libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			var err error
			kadDHT, err = dht.New(ctx, h)
			return kadDHT, err
		}),
	}

	if cfg.EnableRelay {
		opts = append(opts, libp2p.EnableRelay())
	}

	if cfg.EnableHolePunch {
		opts = append(opts, libp2p.EnableHolePunching())
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("starting libp2p node: %w", err)
	}

	if err := kadDHT.Bootstrap(ctx); err != nil {
		log.Warn().Err(err).Msg("DHT bootstrap failed")
	}

	n := &Node{
		Host: h,
		DHT:  kadDHT,
	}

	// Connect to bootstrap peers.
	if len(cfg.BootstrapPeers) > 0 {
		n.connectBootstrapPeers(ctx, cfg.BootstrapPeers)
	}

	log.Info().
		Str("peer_id", h.ID().String()).
		Strs("addrs", n.Addresses()).
		Msg("p2p node started")

	return n, nil
}

// connectBootstrapPeers dials the provided multiaddr bootstrap peers in
// parallel and logs successes/failures.
func (n *Node) connectBootstrapPeers(ctx context.Context, peers []string) {
	var wg sync.WaitGroup
	for _, p := range peers {
		ma, err := multiaddr.NewMultiaddr(p)
		if err != nil {
			log.Warn().Str("addr", p).Err(err).Msg("invalid bootstrap multiaddr")
			continue
		}

		info, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			log.Warn().Str("addr", p).Err(err).Msg("failed to parse peer info")
			continue
		}

		wg.Add(1)
		go func(pi peer.AddrInfo) {
			defer wg.Done()
			connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			if err := n.Host.Connect(connectCtx, pi); err != nil {
				log.Warn().Str("peer", pi.ID.String()).Err(err).Msg("failed to connect to bootstrap peer")
			} else {
				log.Info().Str("peer", pi.ID.String()).Msg("connected to bootstrap peer")
			}
		}(*info)
	}
	wg.Wait()
}

// Addresses returns the full multiaddrs (with peer ID) that this node is
// listening on.
func (n *Node) Addresses() []string {
	var addrs []string
	for _, a := range n.Host.Addrs() {
		addrs = append(addrs, fmt.Sprintf("%s/p2p/%s", a.String(), n.Host.ID().String()))
	}
	return addrs
}

// ConnectedPeers returns the peer IDs currently connected to this node.
func (n *Node) ConnectedPeers() []peer.ID {
	return n.Host.Network().Peers()
}

// FindPeer uses the DHT to look up a specific peer by its ID.
func (n *Node) FindPeer(ctx context.Context, id peer.ID) (peer.AddrInfo, error) {
	return n.DHT.FindPeer(ctx, id)
}

// Close gracefully shuts down the DHT and host.
func (n *Node) Close() error {
	if n.DHT != nil {
		_ = n.DHT.Close()
	}
	return n.Host.Close()
}
