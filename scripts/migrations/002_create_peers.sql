CREATE TABLE IF NOT EXISTS peers (
    node_id    TEXT PRIMARY KEY,
    addrs      TEXT[]       NOT NULL,
    public_key TEXT         NOT NULL DEFAULT '',
    is_relay   BOOLEAN      NOT NULL DEFAULT FALSE,
    last_seen  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
