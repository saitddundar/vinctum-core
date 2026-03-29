package relay

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	relayv1 "github.com/saitddundar/vinctum-core/proto/relay/v1"
)

type Client struct {
	pool *PeerPool
}

func NewClient(pool *PeerPool) *Client {
	return &Client{pool: pool}
}

func (c *Client) ForwardChunk(ctx context.Context, nodeID string, req *relayv1.RelayChunkRequest) (*relayv1.RelayChunkResponse, error) {
	cb := c.pool.GetBreaker(nodeID)
	if err := cb.Allow(); err != nil {
		return nil, fmt.Errorf("node %s: %w", nodeID, err)
	}

	conn, err := c.pool.GetConn(ctx, nodeID)
	if err != nil {
		cb.RecordFailure()
		return nil, err
	}

	client := relayv1.NewRelayServiceClient(conn)
	resp, err := client.RelayChunk(ctx, req)
	if err != nil {
		cb.RecordFailure()
		log.Warn().Str("node_id", nodeID).Err(err).Msg("relay chunk failed")
		return nil, err
	}

	cb.RecordSuccess()
	return resp, nil
}

func (c *Client) FetchChunk(ctx context.Context, nodeID string, req *relayv1.FetchChunkRequest) (*relayv1.FetchChunkResponse, error) {
	cb := c.pool.GetBreaker(nodeID)
	if err := cb.Allow(); err != nil {
		return nil, fmt.Errorf("node %s: %w", nodeID, err)
	}

	conn, err := c.pool.GetConn(ctx, nodeID)
	if err != nil {
		cb.RecordFailure()
		return nil, err
	}

	client := relayv1.NewRelayServiceClient(conn)
	resp, err := client.FetchChunk(ctx, req)
	if err != nil {
		cb.RecordFailure()
		return nil, err
	}

	cb.RecordSuccess()
	return resp, nil
}

func (c *Client) PingNode(ctx context.Context, nodeID string) (time.Duration, error) {
	cb := c.pool.GetBreaker(nodeID)
	if err := cb.Allow(); err != nil {
		return 0, fmt.Errorf("node %s: %w", nodeID, err)
	}

	conn, err := c.pool.GetConn(ctx, nodeID)
	if err != nil {
		cb.RecordFailure()
		return 0, err
	}

	client := relayv1.NewRelayServiceClient(conn)
	start := time.Now()
	_, err = client.Ping(ctx, &relayv1.PingRequest{NodeId: nodeID})
	if err != nil {
		cb.RecordFailure()
		return 0, err
	}

	cb.RecordSuccess()
	return time.Since(start), nil
}

func (c *Client) IsNodeHealthy(nodeID string) bool {
	return !c.pool.GetBreaker(nodeID).IsOpen()
}

func (c *Client) Pool() *PeerPool {
	return c.pool
}
