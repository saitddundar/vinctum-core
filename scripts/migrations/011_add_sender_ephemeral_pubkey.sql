ALTER TABLE transfers ADD COLUMN IF NOT EXISTS sender_ephemeral_pubkey BYTEA NOT NULL DEFAULT '';
