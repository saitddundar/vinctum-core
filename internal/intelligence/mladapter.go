package intelligence

import (
	"github.com/rs/zerolog/log"
)

// MLRouterAdapter implements NodeIntelligence using the ML API.
// Falls back to local scoring when ML API is unavailable.
type MLRouterAdapter struct {
	ml        *MLClient
	collector *Collector
	local     *RouterAdapter
}

func NewMLRouterAdapter(ml *MLClient, collector *Collector, local *RouterAdapter) *MLRouterAdapter {
	return &MLRouterAdapter{
		ml:        ml,
		collector: collector,
		local:     local,
	}
}

func (a *MLRouterAdapter) ScoreNode(nodeID string) (float64, bool) {
	m := a.collector.Metrics(nodeID)
	if m == nil {
		return 0, false
	}

	resp, err := a.ml.ScoreNode(nodeID, m)
	if err != nil {
		log.Warn().Err(err).Str("node", nodeID).Msg("ml score failed, falling back to local")
		return a.local.ScoreNode(nodeID)
	}

	return resp.Score, true
}

func (a *MLRouterAdapter) IsAnomalous(nodeID string) bool {
	m := a.collector.Metrics(nodeID)
	if m == nil {
		return false
	}

	resp, err := a.ml.DetectAnomaly(nodeID, m)
	if err != nil {
		log.Warn().Err(err).Str("node", nodeID).Msg("ml anomaly failed, falling back to local")
		return a.local.IsAnomalous(nodeID)
	}

	return resp.IsAnomaly
}
