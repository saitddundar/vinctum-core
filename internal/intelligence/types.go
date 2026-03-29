package intelligence

import "time"

type EventType int

const (
	EventRelaySuccess EventType = iota
	EventRelayFailure
	EventRelayTimeout
	EventPingSuccess
	EventPingFailure
	EventReroute
	EventCircuitOpen
)

type Event struct {
	NodeID    string
	Type      EventType
	LatencyMs int64 // observed latency (0 if not applicable)
	DataBytes int64 // bytes transferred (0 if not applicable)
	Timestamp time.Time
}

type NodeMetrics struct {
	NodeID string

	// Counters.
	TotalEvents  int64
	Successes    int64
	Failures     int64
	Timeouts     int64
	Reroutes     int64
	CircuitOpens int64

	// Latency stats (milliseconds).
	AvgLatencyMs float64
	MinLatencyMs int64
	MaxLatencyMs int64
	P95LatencyMs int64

	// Throughput.
	TotalBytes    int64
	AvgBytesPerOp float64

	// Derived.
	FailureRate float64 // failures / total events (0.0 - 1.0)
	Uptime      float64 // 1.0 - failure_rate (simplified)

	// Time range covered.
	WindowStart time.Time
	WindowEnd   time.Time
}

type ScoreResult struct {
	NodeID     string
	Score      float64 // 0.0 (worst) to 1.0 (best)
	Confidence float64 // 0.0 (no data) to 1.0 (lots of data)
	Reason     string  // human-readable explanation
}

type AnomalyAlert struct {
	NodeID    string
	Type      AnomalyType
	Severity  float64 // 0.0 (mild) to 1.0 (critical)
	Detail    string
	Metric    string  // which metric triggered (e.g. "latency", "failure_rate")
	Value     float64 // observed value
	Threshold float64 // expected boundary
	Timestamp time.Time
}

type AnomalyType int

const (
	AnomalyLatencySpike AnomalyType = iota
	AnomalyHighFailureRate
	AnomalyTrafficSpike
	AnomalyNodeUnresponsive
)

func (a AnomalyType) String() string {
	switch a {
	case AnomalyLatencySpike:
		return "latency_spike"
	case AnomalyHighFailureRate:
		return "high_failure_rate"
	case AnomalyTrafficSpike:
		return "traffic_spike"
	case AnomalyNodeUnresponsive:
		return "node_unresponsive"
	default:
		return "unknown"
	}
}
