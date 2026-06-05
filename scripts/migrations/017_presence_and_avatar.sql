-- Presence: tracks online/offline status of devices via heartbeat
CREATE TABLE IF NOT EXISTS presence (
    device_id   TEXT        NOT NULL PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_presence_user ON presence (user_id, last_seen DESC);

-- Avatar: base64-encoded profile picture stored in DB for simplicity
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_data TEXT DEFAULT NULL;
