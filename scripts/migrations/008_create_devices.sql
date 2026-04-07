CREATE TABLE IF NOT EXISTS devices (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT         NOT NULL,
    device_type   TEXT         NOT NULL DEFAULT 'pc',
    node_id       TEXT,
    fingerprint   TEXT         NOT NULL,
    is_approved   BOOLEAN      NOT NULL DEFAULT FALSE,
    approved_at   TIMESTAMPTZ,
    approved_by   UUID         REFERENCES devices(id),
    last_active   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    revoked_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_devices_user_id ON devices (user_id) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_devices_node_id ON devices (node_id) WHERE node_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_devices_fingerprint ON devices (user_id, fingerprint) WHERE revoked_at IS NULL;
