package intelligence

import (
	"fmt"
	"math"
)

type ScorerWeights struct {
	Uptime     float64 // weight for uptime (1 - failure_rate)
	Latency    float64 // weight for latency (lower is better)
	Throughput float64 // weight for throughput (higher is better)
	Stability  float64 // weight for stability (low reroutes + circuit opens)
}

func DefaultWeights() ScorerWeights {
	return ScorerWeights{
		Uptime:     0.40,
		Latency:    0.30,
		Throughput: 0.15,
		Stability:  0.15,
	}
}

type Scorer struct {
	collector    *Collector
	weights      ScorerWeights
	minEvents    int64   // minimum events required for full confidence
	maxLatencyMs float64 // latency above this gets score 0
}

func NewScorer(c *Collector, w ScorerWeights) *Scorer {
	return &Scorer{
		collector:    c,
		weights:      w,
		minEvents:    50,
		maxLatencyMs: 5000,
	}
}

func (s *Scorer) Score(nodeID string) *ScoreResult {
	m := s.collector.Metrics(nodeID)
	if m == nil {
		return nil
	}
	return s.scoreFromMetrics(m)
}

func (s *Scorer) ScoreAll() map[string]*ScoreResult {
	all := s.collector.AllMetrics()
	result := make(map[string]*ScoreResult, len(all))
	for id, m := range all {
		result[id] = s.scoreFromMetrics(m)
	}
	return result
}

func (s *Scorer) ScoreRoute(nodeIDs []string) *ScoreResult {
	if len(nodeIDs) == 0 {
		return &ScoreResult{Score: 0, Confidence: 0, Reason: "empty route"}
	}

	routeScore := 1.0
	minConfidence := 1.0
	unknownNodes := 0

	for _, id := range nodeIDs {
		ns := s.Score(id)
		if ns == nil {
			unknownNodes++
			routeScore *= 0.5 // unknown nodes get a neutral penalty
			minConfidence = 0
			continue
		}
		routeScore *= ns.Score
		if ns.Confidence < minConfidence {
			minConfidence = ns.Confidence
		}
	}

	reason := fmt.Sprintf("%d hops", len(nodeIDs))
	if unknownNodes > 0 {
		reason += fmt.Sprintf(", %d unknown", unknownNodes)
	}

	return &ScoreResult{
		Score:      routeScore,
		Confidence: minConfidence,
		Reason:     reason,
	}
}

// RankNodes sorts node IDs by score descending, returning scored results.
func (s *Scorer) RankNodes(nodeIDs []string) []*ScoreResult {
	results := make([]*ScoreResult, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		sr := s.Score(id)
		if sr == nil {
			sr = &ScoreResult{
				NodeID:     id,
				Score:      0.5,
				Confidence: 0,
				Reason:     "no data",
			}
		}
		results = append(results, sr)
	}

	// Sort by score descending.
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
	return results
}

func (s *Scorer) scoreFromMetrics(m *NodeMetrics) *ScoreResult {
	// 1. Uptime score: directly use uptime (0-1).
	uptimeScore := m.Uptime

	// 2. Latency score: inverse normalized against maxLatencyMs.
	//    0ms -> 1.0, maxLatencyMs -> 0.0
	latencyScore := 1.0
	if m.AvgLatencyMs > 0 {
		latencyScore = 1.0 - math.Min(m.AvgLatencyMs/s.maxLatencyMs, 1.0)
	}

	// 3. Throughput score: normalized log scale.
	//    More bytes per operation is better, diminishing returns.
	throughputScore := 0.5 // neutral default
	if m.AvgBytesPerOp > 0 {
		// log2(bytes/1024) normalized to 0-1 range, capped.
		throughputScore = math.Min(math.Log2(m.AvgBytesPerOp/1024+1)/10, 1.0)
	}

	// 4. Stability score: penalize reroutes and circuit opens.
	stabilityScore := 1.0
	if m.TotalEvents > 0 {
		instabilityRate := float64(m.Reroutes+m.CircuitOpens) / float64(m.TotalEvents)
		stabilityScore = 1.0 - math.Min(instabilityRate*5, 1.0) // 20% instability events = score 0
	}

	// Weighted sum.
	w := s.weights
	score := w.Uptime*uptimeScore +
		w.Latency*latencyScore +
		w.Throughput*throughputScore +
		w.Stability*stabilityScore

	// Clamp to [0, 1].
	score = math.Max(0, math.Min(score, 1.0))

	// Confidence: based on how much data we have.
	confidence := math.Min(float64(m.TotalEvents)/float64(s.minEvents), 1.0)

	reason := fmt.Sprintf("uptime=%.2f lat=%.0fms fail_rate=%.1f%%",
		m.Uptime, m.AvgLatencyMs, m.FailureRate*100)

	return &ScoreResult{
		NodeID:     m.NodeID,
		Score:      score,
		Confidence: confidence,
		Reason:     reason,
	}
}
