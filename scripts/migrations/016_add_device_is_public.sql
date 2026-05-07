ALTER TABLE devices ADD COLUMN IF NOT EXISTS is_public BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_devices_public ON devices (user_id, is_public) WHERE revoked_at IS NULL AND is_approved = TRUE;
