package intelligence

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectorRecord(t *testing.T) {
	c := NewCollector(10 * time.Minute)

	c.RecordRelay("node-A", true, 50, 1024)
	c.RecordRelay("node-A", true, 60, 2048)
	c.RecordRelay("node-A", false, 0, 0)

	assert.Equal(t, 1, c.NodeCount())

	m := c.Metrics("node-A")
	require.NotNil(t, m)
	assert.Equal(t, int64(3), m.TotalEvents)
	assert.Equal(t, int64(2), m.Successes)
	assert.Equal(t, int64(1), m.Failures)
	assert.Equal(t, int64(3072), m.TotalBytes)
}

func TestCollectorMetricsLatency(t *testing.T) {
	c := NewCollector(10 * time.Minute)

	c.RecordRelay("node-B", true, 10, 0)
	c.RecordRelay("node-B", true, 20, 0)
	c.RecordRelay("node-B", true, 30, 0)
	c.RecordRelay("node-B", true, 100, 0)

	m := c.Metrics("node-B")
	require.NotNil(t, m)
	assert.Equal(t, int64(10), m.MinLatencyMs)
	assert.Equal(t, int64(100), m.MaxLatencyMs)
	assert.InDelta(t, 40.0, m.AvgLatencyMs, 0.1)
	assert.Equal(t, int64(100), m.P95LatencyMs)
}

func TestCollectorMetricsFailureRate(t *testing.T) {
	c := NewCollector(10 * time.Minute)

	// 7 successes, 3 failures -> 30% failure rate
	for range 7 {
		c.RecordRelay("node-C", true, 20, 100)
	}
	for range 3 {
		c.RecordRelay("node-C", false, 0, 0)
	}

	m := c.Metrics("node-C")
	require.NotNil(t, m)
	assert.InDelta(t, 0.3, m.FailureRate, 0.01)
	assert.InDelta(t, 0.7, m.Uptime, 0.01)
}

func TestCollectorPing(t *testing.T) {
	c := NewCollector(10 * time.Minute)

	c.RecordPing("node-D", 5, true)
	c.RecordPing("node-D", 8, true)
	c.RecordPing("node-D", 0, false)

	m := c.Metrics("node-D")
	require.NotNil(t, m)
	assert.Equal(t, int64(3), m.TotalEvents)
	assert.Equal(t, int64(2), m.Successes)
	assert.Equal(t, int64(1), m.Failures)
	assert.Equal(t, int64(5), m.MinLatencyMs)
}

func TestCollectorSlidingWindow(t *testing.T) {
	c := NewCollector(1 * time.Second)

	// Record an old event outside the window.
	c.Record(Event{
		NodeID:    "node-E",
		Type:      EventRelaySuccess,
		LatencyMs: 50,
		Timestamp: time.Now().Add(-5 * time.Second),
	})

	// Record a recent event inside the window.
	c.RecordRelay("node-E", true, 30, 0)

	m := c.Metrics("node-E")
	require.NotNil(t, m)
	assert.Equal(t, int64(1), m.TotalEvents) // only the recent one
}

func TestCollectorPrune(t *testing.T) {
	c := NewCollector(1 * time.Second)

	// All old events.
	c.Record(Event{
		NodeID:    "node-F",
		Type:      EventRelaySuccess,
		Timestamp: time.Now().Add(-10 * time.Second),
	})
	c.Record(Event{
		NodeID:    "node-G",
		Type:      EventRelaySuccess,
		Timestamp: time.Now().Add(-10 * time.Second),
	})

	// One recent event for node-G.
	c.RecordRelay("node-G", true, 10, 0)

	assert.Equal(t, 2, c.NodeCount())
	c.Prune()

	// node-F should be gone (all events expired), node-G survives.
	assert.Equal(t, 1, c.NodeCount())
	assert.Nil(t, c.Metrics("node-F"))
	assert.NotNil(t, c.Metrics("node-G"))
}

func TestCollectorAllMetrics(t *testing.T) {
	c := NewCollector(10 * time.Minute)

	c.RecordRelay("node-X", true, 10, 0)
	c.RecordRelay("node-Y", true, 20, 0)
	c.RecordRelay("node-Z", false, 0, 0)

	all := c.AllMetrics()
	assert.Len(t, all, 3)
	assert.Contains(t, all, "node-X")
	assert.Contains(t, all, "node-Y")
	assert.Contains(t, all, "node-Z")
}

func TestCollectorEmptyNode(t *testing.T) {
	c := NewCollector(10 * time.Minute)
	assert.Nil(t, c.Metrics("nonexistent"))
}

func TestCollectorIgnoresEmptyNodeID(t *testing.T) {
	c := NewCollector(10 * time.Minute)
	c.RecordRelay("", true, 10, 0)
	assert.Equal(t, 0, c.NodeCount())
}

func TestCollectorEventTypes(t *testing.T) {
	c := NewCollector(10 * time.Minute)

	c.Record(Event{NodeID: "node-H", Type: EventRelayTimeout, LatencyMs: 5000})
	c.Record(Event{NodeID: "node-H", Type: EventReroute})
	c.Record(Event{NodeID: "node-H", Type: EventCircuitOpen})
	c.Record(Event{NodeID: "node-H", Type: EventRelaySuccess, LatencyMs: 20})

	m := c.Metrics("node-H")
	require.NotNil(t, m)
	assert.Equal(t, int64(4), m.TotalEvents)
	assert.Equal(t, int64(1), m.Successes)
	assert.Equal(t, int64(1), m.Timeouts)
	assert.Equal(t, int64(1), m.Reroutes)
	assert.Equal(t, int64(1), m.CircuitOpens)
	// Timeout counts as failure.
	assert.Equal(t, int64(1), m.Failures)
}

func TestPercentile(t *testing.T) {
	data := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	assert.Equal(t, int64(100), percentile(data, 95))
	assert.Equal(t, int64(50), percentile(data, 50))
	assert.Equal(t, int64(10), percentile(data, 1))
	assert.Equal(t, int64(0), percentile(nil, 50))
}
