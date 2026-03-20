-- name: UpsertPeer :exec
INSERT INTO peers (node_id, addrs, public_key, is_relay, last_seen)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (node_id) DO UPDATE
SET addrs      = EXCLUDED.addrs,
    public_key = EXCLUDED.public_key,
    is_relay   = EXCLUDED.is_relay,
    last_seen  = NOW();

-- name: GetPeer :one
SELECT * FROM peers WHERE node_id = $1;

-- name: ListPeers :many
SELECT * FROM peers ORDER BY last_seen DESC;
