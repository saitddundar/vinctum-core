package relay

import (
	"context"
	"sync"

	"github.com/rs/zerolog/log"
	discoveryv1 "github.com/saitddundar/vinctum-core/proto/discovery/v1"
	relayv1 "github.com/saitddundar/vinctum-core/proto/relay/v1"
)

type Replicator struct {
	relayClient     *Client
	discoveryClient discoveryv1.DiscoveryServiceClient
}

func NewReplicator(rc *Client, dc discoveryv1.DiscoveryServiceClient) *Replicator {
	return &Replicator{relayClient: rc, discoveryClient: dc}
}

func (r *Replicator) ReplicateChunk(ctx context.Context, transferID string, chunkIndex int32, data []byte, chunkHash string, replicationFactor int32, excludeNodes []string) {
	if replicationFactor <= 1 || r.discoveryClient == nil {
		return
	}

	// Find candidate peers for replication.
	resp, err := r.discoveryClient.FindPeers(ctx, &discoveryv1.FindPeersRequest{
		NodeId: transferID, // Use as proximity hint
		Count:  replicationFactor * 2,
	})
	if err != nil {
		log.Warn().Err(err).Msg("failed to find peers for replication")
		return
	}

	excluded := make(map[string]bool, len(excludeNodes))
	for _, id := range excludeNodes {
		excluded[id] = true
	}

	// Pick peers that aren't already on the route.
	var targets []string
	for _, peer := range resp.Peers {
		if excluded[peer.NodeId] {
			continue
		}
		targets = append(targets, peer.NodeId)
		if int32(len(targets)) >= replicationFactor-1 {
			break
		}
	}

	if len(targets) == 0 {
		log.Debug().Str("transfer_id", transferID).Msg("no additional peers for replication")
		return
	}

	// Replicate concurrently.
	var wg sync.WaitGroup
	for _, nodeID := range targets {
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			_, err := r.relayClient.ForwardChunk(ctx, target, &relayv1.RelayChunkRequest{
				TransferId:        transferID,
				ChunkIndex:        chunkIndex,
				Data:              data,
				ChunkHash:         chunkHash,
				RemainingHops:     nil, // Empty = store locally
				ReplicationFactor: 1,   // Don't cascade replication
				Ttl:               1,
			})
			if err != nil {
				log.Warn().
					Err(err).
					Str("target", target).
					Str("transfer_id", transferID).
					Int32("chunk", chunkIndex).
					Msg("replica failed")
			} else {
				log.Debug().
					Str("target", target).
					Str("transfer_id", transferID).
					Int32("chunk", chunkIndex).
					Msg("chunk replicated")
			}
		}(nodeID)
	}
	wg.Wait()
}
