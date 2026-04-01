package intelligence

import (
	"math"
	"time"
)

// using z-score based statistical detection.
type AnomalyDetector struct {
	collector *Collector
	config    AnomalyConfig
}

type AnomalyConfig struct {
	LatencyZThreshold    float64 // z-score above which latency is anomalous (default 2.5)
	FailureRateThreshold float64 // absolute failure rate above which node is flagged (default 0.3)
	TrafficZThreshold    float64 // z-score above which traffic volume is anomalous (default 3.0)
	MinEventsRequired    int64   // minimum events before detection kicks in (default 10)
}

func DefaultAnomalyConfig() AnomalyConfig {
	return AnomalyConfig{
		LatencyZThreshold:    2.5,
		FailureRateThreshold: 0.30,
		TrafficZThreshold:    3.0,
		MinEventsRequired:    10,
	}
}

func NewAnomalyDetector(c *Collector, cfg AnomalyConfig) *AnomalyDetector {
	return &AnomalyDetector{collector: c, config: cfg}
}

func (d *AnomalyDetector) Detect() []AnomalyAlert {
	all := d.collector.AllMetrics()
	if len(all) < 2 {
		return nil // need at least 2 nodes to compute stats
	}

	// Compute population statistics.
	var latencies, failRates, bytesPerOp []float64
	for _, m := range all {
		if m.TotalEvents < d.config.MinEventsRequired {
			continue
		}
		latencies = append(latencies, m.AvgLatencyMs)
		failRates = append(failRates, m.FailureRate)
		bytesPerOp = append(bytesPerOp, m.AvgBytesPerOp)
	}

	latMean, latStd := meanStd(latencies)
	trafficMean, trafficStd := meanStd(bytesPerOp)

	var alerts []AnomalyAlert
	now := time.Now()

	for _, m := range all {
		if m.TotalEvents < d.config.MinEventsRequired {
			continue
		}

		// 1. Latency spike detection.
		if latStd > 0 {
			z := (m.AvgLatencyMs - latMean) / latStd
			if z > d.config.LatencyZThreshold {
				alerts = append(alerts, AnomalyAlert{
					NodeID:    m.NodeID,
					Type:      AnomalyLatencySpike,
					Severity:  clamp(z/5.0, 0, 1),
					Detail:    "average latency significantly above network mean",
					Metric:    "avg_latency_ms",
					Value:     m.AvgLatencyMs,
					Threshold: latMean + d.config.LatencyZThreshold*latStd,
					Timestamp: now,
				})
			}
		}

		// 2. High failure rate detection.
		if m.FailureRate > d.config.FailureRateThreshold {
			alerts = append(alerts, AnomalyAlert{
				NodeID:    m.NodeID,
				Type:      AnomalyHighFailureRate,
				Severity:  clamp(m.FailureRate, 0, 1),
				Detail:    "failure rate exceeds threshold",
				Metric:    "failure_rate",
				Value:     m.FailureRate,
				Threshold: d.config.FailureRateThreshold,
				Timestamp: now,
			})
		}

		// 3. Traffic spike detection.
		if trafficStd > 0 {
			z := (m.AvgBytesPerOp - trafficMean) / trafficStd
			if z > d.config.TrafficZThreshold {
				alerts = append(alerts, AnomalyAlert{
					NodeID:    m.NodeID,
					Type:      AnomalyTrafficSpike,
					Severity:  clamp(z/5.0, 0, 1),
					Detail:    "traffic volume significantly above network mean",
					Metric:    "avg_bytes_per_op",
					Value:     m.AvgBytesPerOp,
					Threshold: trafficMean + d.config.TrafficZThreshold*trafficStd,
					Timestamp: now,
				})
			}
		}

		// 4. Unresponsive node: all recent events are failures.
		if m.Successes == 0 && m.Failures > 0 {
			alerts = append(alerts, AnomalyAlert{
				NodeID:    m.NodeID,
				Type:      AnomalyNodeUnresponsive,
				Severity:  1.0,
				Detail:    "node has zero successful operations in the window",
				Metric:    "success_count",
				Value:     0,
				Threshold: 1,
				Timestamp: now,
			})
		}
	}

	return alerts
}

func (d *AnomalyDetector) DetectNode(nodeID string) []AnomalyAlert {
	all := d.Detect()
	var filtered []AnomalyAlert
	for _, a := range all {
		if a.NodeID == nodeID {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

func (d *AnomalyDetector) IsAnomalous(nodeID string) bool {
	return len(d.DetectNode(nodeID)) > 0
}

func meanStd(data []float64) (mean, std float64) {
	n := float64(len(data))
	if n == 0 {
		return 0, 0
	}

	var sum float64
	for _, v := range data {
		sum += v
	}
	mean = sum / n

	var variance float64
	for _, v := range data {
		diff := v - mean
		variance += diff * diff
	}
	variance /= n
	std = math.Sqrt(variance)
	return mean, std
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(v, hi))
}

