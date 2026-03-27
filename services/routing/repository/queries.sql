-- name: UpsertRoute :exec
INSERT INTO routes (node_id, target_node_id, next_hop_id, metric, latency_ms, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (node_id, target_node_id) DO UPDATE
SET next_hop_id = EXCLUDED.next_hop_id,
    metric      = EXCLUDED.metric,
    latency_ms  = EXCLUDED.latency_ms,
    updated_at  = NOW();

-- name: GetRoutesByNodeID :many
SELECT * FROM routes WHERE node_id = $1 ORDER BY metric ASC;

-- name: FindRoute :one
SELECT * FROM routes WHERE node_id = $1 AND target_node_id = $2;

-- name: DeleteRoute :exec
DELETE FROM routes WHERE node_id = $1 AND target_node_id = $2;

-- name: UpsertRelay :exec
INSERT INTO relays (node_id, address, max_circuits, active_circuits, latency_ms, last_seen)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (node_id) DO UPDATE
SET address         = EXCLUDED.address,
    max_circuits    = EXCLUDED.max_circuits,
    active_circuits = EXCLUDED.active_circuits,
    latency_ms      = EXCLUDED.latency_ms,
    last_seen       = NOW();

-- name: ListRelays :many
SELECT * FROM relays ORDER BY latency_ms ASC LIMIT $1;

-- name: GetRelay :one
SELECT * FROM relays WHERE node_id = $1;
