-- Group transfers: one sender uploads chunks once, multiple receivers in a session
-- each get their own wrapped file key to decrypt the shared chunks.

CREATE TABLE group_transfers (
    id              TEXT PRIMARY KEY,
    session_id      UUID NOT NULL REFERENCES peer_sessions(id) ON DELETE CASCADE,
    sender_node_id  TEXT NOT NULL,
    filename        TEXT NOT NULL DEFAULT '',
    total_size_bytes BIGINT NOT NULL DEFAULT 0,
    content_hash    TEXT NOT NULL DEFAULT '',
    chunk_size_bytes INT NOT NULL DEFAULT 262144,
    total_chunks    INT NOT NULL DEFAULT 0,
    sender_ephemeral_pubkey BYTEA NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_group_transfers_session ON group_transfers (session_id);
CREATE INDEX idx_group_transfers_sender ON group_transfers (sender_node_id);

-- Link individual transfers to their parent group transfer.
-- Chunks are stored under group_transfer_id, shared across all recipients.
ALTER TABLE transfers ADD COLUMN group_transfer_id TEXT REFERENCES group_transfers(id) ON DELETE SET NULL;

-- Per-recipient wrapped file key: the random AES key encrypted with
-- ECDH(sender_ephemeral, receiver_static). Receiver unwraps to decrypt shared chunks.
ALTER TABLE transfers ADD COLUMN wrapped_file_key BYTEA NOT NULL DEFAULT '';

CREATE INDEX idx_transfers_group ON transfers (group_transfer_id) WHERE group_transfer_id IS NOT NULL;
