-- name: CreateTransfer :one
INSERT INTO transfers (transfer_id, sender_node_id, receiver_node_id, filename, total_size_bytes, content_hash, chunk_size_bytes, total_chunks, status, encryption_key, route_hops, replication_factor)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
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
