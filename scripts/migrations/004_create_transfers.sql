CREATE TABLE IF NOT EXISTS transfers (
    transfer_id      TEXT PRIMARY KEY,
    sender_node_id   TEXT        NOT NULL,
    receiver_node_id TEXT        NOT NULL,
    filename         TEXT        NOT NULL DEFAULT '',
    total_size_bytes BIGINT      NOT NULL DEFAULT 0,
    content_hash     TEXT        NOT NULL DEFAULT '',
    chunk_size_bytes INT         NOT NULL DEFAULT 262144, -- 256KB
    total_chunks     INT         NOT NULL DEFAULT 0,
    chunks_done      INT         NOT NULL DEFAULT 0,
    status           INT         NOT NULL DEFAULT 1, -- 1=pending
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_transfers_sender   ON transfers (sender_node_id);
CREATE INDEX IF NOT EXISTS idx_transfers_receiver ON transfers (receiver_node_id);
CREATE INDEX IF NOT EXISTS idx_transfers_status   ON transfers (status);
