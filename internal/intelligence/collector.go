package intelligence

import (
	"math"
	"sort"
	"sync"
	"time"
)

type Collector struct {
	mu     sync.RWMutex
	events map[string][]Event // nodeID -> events
	window time.Duration      // how far back to look
	maxPer int                // max events kept per node (memory bound)
}

func NewCollector(window time.Duration) *Collector {
	if window <= 0 {
		window = 10 * time.Minute
	}
	return &Collector{
		events: make(map[string][]Event),
		window: window,
		maxPer: 10000,
	}
}

func (c *Collector) Record(e Event) {
	if e.NodeID == "" {
		return
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	evts := c.events[e.NodeID]
	evts = append(evts, e)

	// Trim if over capacity.
	if len(evts) > c.maxPer {
		evts = evts[len(evts)-c.maxPer:]
	}
	c.events[e.NodeID] = evts
}

func (c *Collector) RecordRelay(nodeID string, success bool, latencyMs int64, dataBytes int64) {
	t := EventRelaySuccess
	if !success {
		t = EventRelayFailure
	}
	c.Record(Event{
		NodeID:    nodeID,
		Type:      t,
		LatencyMs: latencyMs,
		DataBytes: dataBytes,
	})
}

func (c *Collector) RecordPing(nodeID string, latencyMs int64, success bool) {
	t := EventPingSuccess
	if !success {
		t = EventPingFailure
	}
	c.Record(Event{
		NodeID:    nodeID,
		Type:      t,
		LatencyMs: latencyMs,
	})
}

func (c *Collector) Metrics(nodeID string) *NodeMetrics {
	c.mu.RLock()
	raw := c.events[nodeID]
	c.mu.RUnlock()

	if len(raw) == 0 {
		return nil
	}

	cutoff := time.Now().Add(-c.window)
	var filtered []Event
	for _, e := range raw {
		if e.Timestamp.After(cutoff) {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) == 0 {
		return nil
	}

	m := &NodeMetrics{
		NodeID:       nodeID,
		TotalEvents:  int64(len(filtered)),
		MinLatencyMs: math.MaxInt64,
		WindowStart:  filtered[0].Timestamp,
		WindowEnd:    filtered[len(filtered)-1].Timestamp,
	}

	var latencies []int64

	for _, e := range filtered {
		switch e.Type {
		case EventRelaySuccess, EventPingSuccess:
			m.Successes++
		case EventRelayFailure, EventPingFailure:
			m.Failures++
		case EventRelayTimeout:
			m.Timeouts++
			m.Failures++ // timeouts count as failures too
		case EventReroute:
			m.Reroutes++
		case EventCircuitOpen:
			m.CircuitOpens++
		}

		if e.LatencyMs > 0 {
			latencies = append(latencies, e.LatencyMs)
			if e.LatencyMs < m.MinLatencyMs {
				m.MinLatencyMs = e.LatencyMs
			}
			if e.LatencyMs > m.MaxLatencyMs {
				m.MaxLatencyMs = e.LatencyMs
			}
		}

		m.TotalBytes += e.DataBytes
	}

	// Latency aggregation.
	if len(latencies) > 0 {
		var sum int64
		for _, l := range latencies {
			sum += l
		}
		m.AvgLatencyMs = float64(sum) / float64(len(latencies))
		m.P95LatencyMs = percentile(latencies, 95)
	} else {
		m.MinLatencyMs = 0
	}

	// Derived metrics.
	if m.TotalEvents > 0 {
		m.FailureRate = float64(m.Failures) / float64(m.TotalEvents)
		m.Uptime = 1.0 - m.FailureRate
	}

	if m.Successes > 0 {
		m.AvgBytesPerOp = float64(m.TotalBytes) / float64(m.Successes)
	}

	return m
}

func (c *Collector) AllMetrics() map[string]*NodeMetrics {
	c.mu.RLock()
	nodeIDs := make([]string, 0, len(c.events))
	for id := range c.events {
		nodeIDs = append(nodeIDs, id)
	}
	c.mu.RUnlock()

	result := make(map[string]*NodeMetrics, len(nodeIDs))
	for _, id := range nodeIDs {
		if m := c.Metrics(id); m != nil {
			result[id] = m
		}
	}
	return result
}

func (c *Collector) Prune() {
	cutoff := time.Now().Add(-c.window)

	c.mu.Lock()
	defer c.mu.Unlock()

	for id, evts := range c.events {
		start := 0
		for i, e := range evts {
			if e.Timestamp.After(cutoff) {
				start = i
				break
			}
			if i == len(evts)-1 {
				start = len(evts) // all expired
			}
		}
		if start >= len(evts) {
			delete(c.events, id)
		} else if start > 0 {
			c.events[id] = evts[start:]
		}
	}
}

func (c *Collector) NodeCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.events)
}

func percentile(data []int64, p int) int64 {
	if len(data) == 0 {
		return 0
	}
	sorted := make([]int64, len(data))
	copy(sorted, data)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := max(int(math.Ceil(float64(p)/100.0*float64(len(sorted))))-1, 0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
