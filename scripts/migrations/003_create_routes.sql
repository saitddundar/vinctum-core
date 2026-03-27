CREATE TABLE IF NOT EXISTS routes (
    id             SERIAL PRIMARY KEY,
    node_id        TEXT        NOT NULL,
    target_node_id TEXT        NOT NULL,
    next_hop_id    TEXT        NOT NULL,
    metric         INT         NOT NULL DEFAULT 1,
    latency_ms     BIGINT      NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (node_id, target_node_id)
);

CREATE INDEX IF NOT EXISTS idx_routes_node_id ON routes (node_id);

CREATE TABLE IF NOT EXISTS relays (
    node_id         TEXT PRIMARY KEY,
    address         TEXT    NOT NULL,
    max_circuits    INT     NOT NULL DEFAULT 64,
    active_circuits INT     NOT NULL DEFAULT 0,
    latency_ms      BIGINT  NOT NULL DEFAULT 0,
    last_seen       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
