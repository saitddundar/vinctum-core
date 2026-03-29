package intelligence

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedCollector(t *testing.T) *Collector {
	t.Helper()
	c := NewCollector(10 * time.Minute)

	// node-good: 95% success, low latency
	for range 95 {
		c.RecordRelay("node-good", true, 15, 4096)
	}
	for range 5 {
		c.RecordRelay("node-good", false, 0, 0)
	}

	// node-bad: 50% success, high latency
	for range 50 {
		c.RecordRelay("node-bad", true, 800, 1024)
	}
	for range 50 {
		c.RecordRelay("node-bad", false, 0, 0)
	}

	// node-slow: 100% success but very high latency
	for range 100 {
		c.RecordRelay("node-slow", true, 3000, 2048)
	}

	// node-unstable: ok success rate but lots of reroutes
	for range 80 {
		c.RecordRelay("node-unstable", true, 50, 2048)
	}
	for range 10 {
		c.Record(Event{NodeID: "node-unstable", Type: EventReroute})
	}
	for range 10 {
		c.Record(Event{NodeID: "node-unstable", Type: EventCircuitOpen})
	}

	return c
}

func TestScorerGoodNodeHighScore(t *testing.T) {
	c := seedCollector(t)
	s := NewScorer(c, DefaultWeights())

	r := s.Score("node-good")
	require.NotNil(t, r)
	assert.Greater(t, r.Score, 0.8)
	assert.Equal(t, 1.0, r.Confidence) // 100 events >= minEvents(50)
}

func TestScorerBadNodeLowScore(t *testing.T) {
	c := seedCollector(t)
	s := NewScorer(c, DefaultWeights())

	r := s.Score("node-bad")
	require.NotNil(t, r)
	assert.Less(t, r.Score, 0.65)
}

func TestScorerSlowNodePenalized(t *testing.T) {
	c := seedCollector(t)
	s := NewScorer(c, DefaultWeights())

	good := s.Score("node-good")
	slow := s.Score("node-slow")
	require.NotNil(t, good)
	require.NotNil(t, slow)

	// Good node should score higher than slow node despite slow having 100% uptime.
	assert.Greater(t, good.Score, slow.Score)
}

func TestScorerUnstableNodePenalized(t *testing.T) {
	c := seedCollector(t)
	s := NewScorer(c, DefaultWeights())

	good := s.Score("node-good")
	unstable := s.Score("node-unstable")
	require.NotNil(t, good)
	require.NotNil(t, unstable)

	assert.Greater(t, good.Score, unstable.Score)
}

func TestScorerUnknownNode(t *testing.T) {
	c := seedCollector(t)
	s := NewScorer(c, DefaultWeights())

	r := s.Score("nonexistent")
	assert.Nil(t, r)
}

func TestScorerConfidence(t *testing.T) {
	c := NewCollector(10 * time.Minute)
	s := NewScorer(c, DefaultWeights())

	// Only 10 events -> low confidence.
	for range 10 {
		c.RecordRelay("node-few", true, 20, 1024)
	}

	r := s.Score("node-few")
	require.NotNil(t, r)
	assert.InDelta(t, 0.2, r.Confidence, 0.01) // 10/50 = 0.2
}

func TestScoreRoute(t *testing.T) {
	c := seedCollector(t)
	s := NewScorer(c, DefaultWeights())

	t.Run("good route", func(t *testing.T) {
		r := s.ScoreRoute([]string{"node-good"})
		assert.Greater(t, r.Score, 0.8)
	})

	t.Run("route with bad node drags score down", func(t *testing.T) {
		good := s.ScoreRoute([]string{"node-good"})
		mixed := s.ScoreRoute([]string{"node-good", "node-bad"})
		assert.Greater(t, good.Score, mixed.Score)
	})

	t.Run("empty route", func(t *testing.T) {
		r := s.ScoreRoute(nil)
		assert.Equal(t, 0.0, r.Score)
	})

	t.Run("unknown nodes penalized", func(t *testing.T) {
		r := s.ScoreRoute([]string{"node-good", "unknown-1"})
		assert.Contains(t, r.Reason, "unknown")
	})
}

func TestRankNodes(t *testing.T) {
	c := seedCollector(t)
	s := NewScorer(c, DefaultWeights())

	ranked := s.RankNodes([]string{"node-bad", "node-good", "node-slow"})
	require.Len(t, ranked, 3)

	// Best node first.
	assert.Equal(t, "node-good", ranked[0].NodeID)
	// Scores descending.
	assert.GreaterOrEqual(t, ranked[0].Score, ranked[1].Score)
	assert.GreaterOrEqual(t, ranked[1].Score, ranked[2].Score)
}

func TestScoreAll(t *testing.T) {
	c := seedCollector(t)
	s := NewScorer(c, DefaultWeights())

	all := s.ScoreAll()
	assert.Len(t, all, 4)
	assert.Contains(t, all, "node-good")
	assert.Contains(t, all, "node-bad")
	assert.Contains(t, all, "node-slow")
	assert.Contains(t, all, "node-unstable")
}

func TestCustomWeights(t *testing.T) {
	c := seedCollector(t)

	// All weight on latency -- slow node should be worst.
	latencyOnly := ScorerWeights{Latency: 1.0}
	s := NewScorer(c, latencyOnly)

	slow := s.Score("node-slow")
	good := s.Score("node-good")
	require.NotNil(t, slow)
	require.NotNil(t, good)
	assert.Greater(t, good.Score, slow.Score)

	// All weight on uptime -- slow node (100% uptime) should beat bad node (50%).
	uptimeOnly := ScorerWeights{Uptime: 1.0}
	s2 := NewScorer(c, uptimeOnly)

	slow2 := s2.Score("node-slow")
	bad2 := s2.Score("node-bad")
	require.NotNil(t, slow2)
	require.NotNil(t, bad2)
	assert.Greater(t, slow2.Score, bad2.Score)
}
