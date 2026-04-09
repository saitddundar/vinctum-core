package intelligence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type MLClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewMLClient(baseURL, apiKey string) *MLClient {
	return &MLClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

type mlNodeMetrics struct {
	TotalEvents   int64   `json:"total_events"`
	Successes     int64   `json:"successes"`
	Failures      int64   `json:"failures"`
	Timeouts      int64   `json:"timeouts"`
	Reroutes      int64   `json:"reroutes"`
	CircuitOpens  int64   `json:"circuit_opens"`
	AvgLatencyMs  float64 `json:"avg_latency_ms"`
	MinLatencyMs  float64 `json:"min_latency_ms"`
	MaxLatencyMs  float64 `json:"max_latency_ms"`
	P95LatencyMs  float64 `json:"p95_latency_ms"`
	TotalBytes    int64   `json:"total_bytes"`
	AvgBytesPerOp float64 `json:"avg_bytes_per_op"`
	FailureRate   float64 `json:"failure_rate"`
	Uptime        float64 `json:"uptime"`
}

type mlScoreRequest struct {
	NodeID  string        `json:"node_id"`
	Metrics mlNodeMetrics `json:"metrics"`
}

type mlScoreResponse struct {
	NodeID     string  `json:"node_id"`
	Score      float64 `json:"score"`
	Confidence float64 `json:"confidence"`
}

type mlAnomalyRequest struct {
	NodeID          string        `json:"node_id"`
	Metrics         mlNodeMetrics `json:"metrics"`
	EventsPerMinute float64      `json:"events_per_minute"`
}

type mlAnomalyResponse struct {
	NodeID       string  `json:"node_id"`
	IsAnomaly    bool    `json:"is_anomaly"`
	AnomalyScore float64 `json:"anomaly_score"`
}

func metricsToML(m *NodeMetrics) mlNodeMetrics {
	return mlNodeMetrics{
		TotalEvents:   m.TotalEvents,
		Successes:     m.Successes,
		Failures:      m.Failures,
		Timeouts:      m.Timeouts,
		Reroutes:      m.Reroutes,
		CircuitOpens:  m.CircuitOpens,
		AvgLatencyMs:  m.AvgLatencyMs,
		MinLatencyMs:  float64(m.MinLatencyMs),
		MaxLatencyMs:  float64(m.MaxLatencyMs),
		P95LatencyMs:  float64(m.P95LatencyMs),
		TotalBytes:    m.TotalBytes,
		AvgBytesPerOp: m.AvgBytesPerOp,
		FailureRate:   m.FailureRate,
		Uptime:        m.Uptime,
	}
}

func (c *MLClient) ScoreNode(nodeID string, m *NodeMetrics) (*mlScoreResponse, error) {
	req := mlScoreRequest{
		NodeID:  nodeID,
		Metrics: metricsToML(m),
	}

	var resp mlScoreResponse
	if err := c.post("/score", req, &resp); err != nil {
		return nil, fmt.Errorf("ml score: %w", err)
	}
	return &resp, nil
}

func (c *MLClient) DetectAnomaly(nodeID string, m *NodeMetrics) (*mlAnomalyResponse, error) {
	windowMinutes := m.WindowEnd.Sub(m.WindowStart).Minutes()
	epm := 0.0
	if windowMinutes > 0 {
		epm = float64(m.TotalEvents) / windowMinutes
	}

	req := mlAnomalyRequest{
		NodeID:          nodeID,
		Metrics:         metricsToML(m),
		EventsPerMinute: epm,
	}

	var resp mlAnomalyResponse
	if err := c.post("/anomaly", req, &resp); err != nil {
		return nil, fmt.Errorf("ml anomaly: %w", err)
	}
	return &resp, nil
}

func (c *MLClient) post(path string, body any, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ml api returned %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(result)
}
