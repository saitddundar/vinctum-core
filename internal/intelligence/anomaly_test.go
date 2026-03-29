package intelligence

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAnomalyCollector(t *testing.T) *Collector {
	t.Helper()
	c := NewCollector(10 * time.Minute)

	// 3 normal nodes with similar latency (~20ms) and low failure rate.
	for _, id := range []string{"normal-1", "normal-2", "normal-3"} {
		for range 50 {
			c.RecordRelay(id, true, 20, 2048)
		}
		for range 2 {
			c.RecordRelay(id, false, 0, 0)
		}
	}

	// 1 node with extremely high latency (anomalous).
	for range 50 {
		c.RecordRelay("slow-outlier", true, 4000, 2048)
	}

	// 1 node with high failure rate.
	for range 10 {
		c.RecordRelay("failing-node", true, 25, 1024)
	}
	for range 40 {
		c.RecordRelay("failing-node", false, 0, 0)
	}

	// 1 completely dead node.
	for range 20 {
		c.RecordRelay("dead-node", false, 0, 0)
	}

	// 1 node with abnormally high traffic.
	for range 50 {
		c.RecordRelay("traffic-hog", true, 20, 500000)
	}

	return c
}

func TestDetectLatencySpike(t *testing.T) {
	c := setupAnomalyCollector(t)
	cfg := DefaultAnomalyConfig()
	cfg.LatencyZThreshold = 2.0 // lower threshold for small population
	d := NewAnomalyDetector(c, cfg)

	alerts := d.DetectNode("slow-outlier")
	require.NotEmpty(t, alerts)

	found := false
	for _, a := range alerts {
		if a.Type == AnomalyLatencySpike {
			found = true
			assert.Greater(t, a.Severity, 0.0)
			assert.Equal(t, "avg_latency_ms", a.Metric)
		}
	}
	assert.True(t, found, "expected latency spike alert for slow-outlier")
}

func TestDetectHighFailureRate(t *testing.T) {
	c := setupAnomalyCollector(t)
	d := NewAnomalyDetector(c, DefaultAnomalyConfig())

	alerts := d.DetectNode("failing-node")
	require.NotEmpty(t, alerts)

	found := false
	for _, a := range alerts {
		if a.Type == AnomalyHighFailureRate {
			found = true
			assert.Greater(t, a.Value, 0.3)
		}
	}
	assert.True(t, found, "expected high failure rate alert for failing-node")
}

func TestDetectUnresponsiveNode(t *testing.T) {
	c := setupAnomalyCollector(t)
	d := NewAnomalyDetector(c, DefaultAnomalyConfig())

	alerts := d.DetectNode("dead-node")
	require.NotEmpty(t, alerts)

	found := false
	for _, a := range alerts {
		if a.Type == AnomalyNodeUnresponsive {
			found = true
			assert.Equal(t, 1.0, a.Severity)
		}
	}
	assert.True(t, found, "expected unresponsive alert for dead-node")
}

func TestDetectTrafficSpike(t *testing.T) {
	c := setupAnomalyCollector(t)
	cfg := DefaultAnomalyConfig()
	cfg.TrafficZThreshold = 2.0 // lower threshold for small population
	d := NewAnomalyDetector(c, cfg)

	alerts := d.DetectNode("traffic-hog")
	require.NotEmpty(t, alerts)

	found := false
	for _, a := range alerts {
		if a.Type == AnomalyTrafficSpike {
			found = true
			assert.Equal(t, "avg_bytes_per_op", a.Metric)
		}
	}
	assert.True(t, found, "expected traffic spike alert for traffic-hog")
}

func TestNormalNodesNoAlerts(t *testing.T) {
	c := setupAnomalyCollector(t)
	d := NewAnomalyDetector(c, DefaultAnomalyConfig())

	for _, id := range []string{"normal-1", "normal-2", "normal-3"} {
		alerts := d.DetectNode(id)
		assert.Empty(t, alerts, "expected no alerts for %s", id)
	}
}

func TestIsAnomalous(t *testing.T) {
	c := setupAnomalyCollector(t)
	d := NewAnomalyDetector(c, DefaultAnomalyConfig())

	assert.True(t, d.IsAnomalous("dead-node"))
	assert.True(t, d.IsAnomalous("failing-node"))
	assert.False(t, d.IsAnomalous("normal-1"))
	assert.False(t, d.IsAnomalous("nonexistent"))
}

func TestDetectNeedsMinimumNodes(t *testing.T) {
	c := NewCollector(10 * time.Minute)
	d := NewAnomalyDetector(c, DefaultAnomalyConfig())

	// Only 1 node -- not enough for population stats.
	for range 50 {
		c.RecordRelay("solo-node", true, 20, 1024)
	}

	alerts := d.Detect()
	assert.Empty(t, alerts)
}

func TestDetectRespectsMinEvents(t *testing.T) {
	c := NewCollector(10 * time.Minute)
	d := NewAnomalyDetector(c, DefaultAnomalyConfig())

	// Two nodes, but one has too few events.
	for range 50 {
		c.RecordRelay("enough-data", true, 20, 1024)
	}
	for range 5 {
		c.RecordRelay("too-few", false, 0, 0) // only 5 events < minEventsRequired(10)
	}

	alerts := d.Detect()
	for _, a := range alerts {
		assert.NotEqual(t, "too-few", a.NodeID)
	}
}

func TestCustomAnomalyConfig(t *testing.T) {
	c := setupAnomalyCollector(t)

	// Very strict config -- even normal failure rates should be caught.
	strict := AnomalyConfig{
		LatencyZThreshold:    1.0,
		FailureRateThreshold: 0.01, // 1% failure rate triggers alert
		TrafficZThreshold:    1.0,
		MinEventsRequired:    5,
	}
	d := NewAnomalyDetector(c, strict)

	// Normal nodes have ~4% failure rate (2/52), should now be flagged.
	alerts := d.DetectNode("normal-1")
	assert.NotEmpty(t, alerts)
}

func TestMeanStd(t *testing.T) {
	mean, std := meanStd([]float64{10, 20, 30, 40, 50})
	assert.InDelta(t, 30.0, mean, 0.01)
	assert.InDelta(t, 14.14, std, 0.1)

	mean, std = meanStd(nil)
	assert.Equal(t, 0.0, mean)
	assert.Equal(t, 0.0, std)
}
