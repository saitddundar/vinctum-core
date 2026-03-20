package p2p

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/rs/zerolog/log"
)

type NodeConfig struct {
	ListenAddrs []string
	PrivateKey  []byte // Raw Ed25519 or RSA depending on crypto implementation
}

type Node struct {
	Host host.Host
	DHT  *dht.IpfsDHT
}

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

	log.Info().
		Str("peer_id", h.ID().String()).
		Strs("addrs", n.Addresses()).
		Msg("p2p node started")

	return n, nil
}

func (n *Node) Addresses() []string {
	var addrs []string
	for _, a := range n.Host.Addrs() {
		addrs = append(addrs, fmt.Sprintf("%s/p2p/%s", a.String(), n.Host.ID().String()))
	}
	return addrs
}

func (n *Node) Close() error {
	if n.DHT != nil {
		_ = n.DHT.Close()
	}
	return n.Host.Close()
}
