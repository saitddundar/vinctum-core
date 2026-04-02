package intelligence

type RouterAdapter struct {
	scorer   *Scorer
	detector *AnomalyDetector
}

func NewRouterAdapter(s *Scorer, d *AnomalyDetector) *RouterAdapter {
	return &RouterAdapter{scorer: s, detector: d}
}

func (a *RouterAdapter) ScoreNode(nodeID string) (float64, bool) {
	sr := a.scorer.Score(nodeID)
	if sr == nil {
		return 0, false
	}
	return sr.Score, true
}

func (a *RouterAdapter) IsAnomalous(nodeID string) bool {
	return a.detector.IsAnomalous(nodeID)
}
