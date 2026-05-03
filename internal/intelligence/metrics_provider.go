package intelligence

// CollectorMetricsProvider adapts the Collector to the routing handler's MetricsProvider interface.
type CollectorMetricsProvider struct {
	collector *Collector
}

func NewCollectorMetricsProvider(c *Collector) *CollectorMetricsProvider {
	return &CollectorMetricsProvider{collector: c}
}

func (p *CollectorMetricsProvider) NodeMetrics(nodeID string) (totalEvents, successes, failures, timeouts, reroutes, circuitOpens int64, avgLatencyMs, minLatencyMs, maxLatencyMs, p95LatencyMs float64, totalBytes int64, avgBytesPerOp, failureRate, uptime float64, ok bool) {
	m := p.collector.Metrics(nodeID)
	if m == nil {
		return 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, false
	}
	return m.TotalEvents, m.Successes, m.Failures, m.Timeouts, m.Reroutes, m.CircuitOpens,
		m.AvgLatencyMs, float64(m.MinLatencyMs), float64(m.MaxLatencyMs), float64(m.P95LatencyMs),
		m.TotalBytes, m.AvgBytesPerOp, m.FailureRate, m.Uptime, true
}

func (p *CollectorMetricsProvider) AllNodeIDs() []string {
	all := p.collector.AllMetrics()
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	return ids
}
