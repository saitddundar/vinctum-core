-- name: CreateTransfer :one
INSERT INTO transfers (transfer_id, sender_node_id, receiver_node_id, filename, total_size_bytes, content_hash, chunk_size_bytes, total_chunks, status, encryption_key, route_hops, replication_factor, sender_ephemeral_pubkey, group_transfer_id, wrapped_file_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING *;

-- name: GetTransfer :one
SELECT * FROM transfers WHERE transfer_id = $1;

-- name: ListTransfersByNode :many
SELECT * FROM transfers WHERE sender_node_id = $1 OR receiver_node_id = $1 ORDER BY created_at DESC;

-- name: ListTransfersByStatus :many
SELECT * FROM transfers WHERE (sender_node_id = $1 OR receiver_node_id = $1) AND status = $2 ORDER BY created_at DESC;

-- name: UpdateTransferProgress :exec
UPDATE transfers SET chunks_done = $2, updated_at = NOW() WHERE transfer_id = $1;

-- name: UpdateTransferStatus :exec
UPDATE transfers SET status = $2, updated_at = NOW() WHERE transfer_id = $1;

-- name: CompleteTransfer :exec
UPDATE transfers SET status = 3, chunks_done = total_chunks, updated_at = NOW() WHERE transfer_id = $1;

-- name: UpdateRouteHops :exec
UPDATE transfers SET route_hops = $2, updated_at = NOW() WHERE transfer_id = $1;

-- name: UpdateTransferMode :exec
UPDATE transfers SET transfer_mode = $2, updated_at = NOW() WHERE transfer_id = $1;

-- name: ConfirmP2PTransfer :exec
UPDATE transfers SET status = $2, chunks_done = $3, transfer_mode = $4, updated_at = NOW() WHERE transfer_id = $1;

-- ─── Group Transfers ────────────────────────────────────

-- name: CreateGroupTransfer :one
INSERT INTO group_transfers (id, session_id, sender_node_id, filename, total_size_bytes, content_hash, chunk_size_bytes, total_chunks, sender_ephemeral_pubkey)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetGroupTransfer :one
SELECT * FROM group_transfers WHERE id = $1;

-- name: ListGroupTransfersBySession :many
SELECT * FROM group_transfers WHERE session_id = $1 ORDER BY created_at DESC;

-- name: ListTransfersByGroupID :many
SELECT * FROM transfers WHERE group_transfer_id = $1 ORDER BY created_at ASC;

-- ─── Platform Stats ────────────────────────────────

-- name: CountTransfers :one
SELECT COUNT(*) FROM transfers;

-- name: SumTransferredBytes :one
SELECT COALESCE(SUM(total_size_bytes), 0)::bigint FROM transfers WHERE status = 3;

-- name: AdminListTransfers :many
SELECT * FROM transfers ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- ─── Activity Heatmap ─────────────────────────────

-- name: GetTransferActivityHeatmap :many
SELECT
    DATE(created_at AT TIME ZONE 'UTC') AS day,
    COUNT(*) AS transfer_count
FROM transfers
WHERE (sender_node_id = $1 OR receiver_node_id = $1)
  AND created_at >= NOW() - INTERVAL '364 days'
GROUP BY DATE(created_at AT TIME ZONE 'UTC')
ORDER BY day ASC;

-- ─── Speed Stats ──────────────────────────────────

-- name: GetTransferSpeedStats :one
SELECT
    COALESCE(SUM(total_size_bytes), 0)::bigint AS bytes_last_minute,
    COUNT(*) AS transfers_last_minute
FROM transfers
WHERE (sender_node_id = $1 OR receiver_node_id = $1)
  AND status = 3
  AND updated_at >= NOW() - INTERVAL '60 seconds';

