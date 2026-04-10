CREATE TABLE IF NOT EXISTS device_keys (
    device_id        UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    kex_algo         TEXT         NOT NULL DEFAULT 'x25519',
    kex_public_key   BYTEA        NOT NULL,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    rotated_at       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_device_keys_algo ON device_keys (kex_algo);
