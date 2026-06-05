-- Index for activity heatmap queries: count transfers per day efficiently
CREATE INDEX IF NOT EXISTS idx_transfers_created_node ON transfers (sender_node_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_transfers_received_node ON transfers (receiver_node_id, created_at DESC);
