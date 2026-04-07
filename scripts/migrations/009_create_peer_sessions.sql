CREATE TABLE IF NOT EXISTS peer_sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT         NOT NULL DEFAULT 'Default Session',
    is_active   BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    closed_at   TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS peer_session_devices (
    session_id  UUID NOT NULL REFERENCES peer_sessions(id) ON DELETE CASCADE,
    device_id   UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    left_at     TIMESTAMPTZ,
    PRIMARY KEY (session_id, device_id)
);

CREATE INDEX IF NOT EXISTS idx_peer_sessions_user_id ON peer_sessions (user_id) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_peer_session_devices_device ON peer_session_devices (device_id) WHERE left_at IS NULL;
