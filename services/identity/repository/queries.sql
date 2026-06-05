-- name: CreateUser :one
INSERT INTO users (username, email, password_hash)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1::uuid;

-- name: SetVerificationToken :exec
UPDATE users
SET verification_token = $2, verification_expires_at = $3
WHERE id = $1::uuid;

-- name: GetUserByVerificationToken :one
SELECT * FROM users
WHERE verification_token = $1
  AND verification_expires_at > NOW();

-- name: VerifyUserEmail :exec
UPDATE users
SET email_verified = TRUE, verification_token = NULL, verification_expires_at = NULL
WHERE id = $1::uuid;

-- ─── Devices ────────────────────────────────────────

-- name: CreateDevice :one
INSERT INTO devices (user_id, name, device_type, node_id, fingerprint, is_approved, approved_at, approved_by, is_public)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetDeviceByID :one
SELECT * FROM devices WHERE id = $1::uuid AND revoked_at IS NULL;

-- name: ListDevicesByUser :many
SELECT * FROM devices WHERE user_id = $1::uuid AND revoked_at IS NULL ORDER BY created_at DESC;

-- name: RevokeDevice :exec
UPDATE devices SET revoked_at = NOW() WHERE id = $1::uuid AND user_id = $2::uuid;

-- name: ApproveDevice :exec
UPDATE devices SET is_approved = TRUE, approved_at = NOW(), approved_by = $2::uuid
WHERE id = $1::uuid;

-- name: RejectDevice :exec
UPDATE devices SET revoked_at = NOW() WHERE id = $1::uuid AND is_approved = FALSE;

-- name: UpdateDeviceActivity :exec
UPDATE devices SET last_active = NOW(), node_id = COALESCE($2, node_id)
WHERE id = $1::uuid AND revoked_at IS NULL;

-- name: GetDeviceByFingerprint :one
SELECT * FROM devices WHERE user_id = $1::uuid AND fingerprint = $2 AND revoked_at IS NULL;

-- ─── Peer Sessions ──────────────────────────────────

-- name: CreatePeerSession :one
INSERT INTO peer_sessions (user_id, name) VALUES ($1, $2) RETURNING *;

-- name: GetPeerSession :one
SELECT * FROM peer_sessions WHERE id = $1::uuid;

-- name: ListActivePeerSessions :many
SELECT * FROM peer_sessions WHERE user_id = $1::uuid AND is_active = TRUE ORDER BY created_at DESC;

-- name: ClosePeerSession :exec
UPDATE peer_sessions SET is_active = FALSE, closed_at = NOW() WHERE id = $1::uuid AND user_id = $2::uuid;

-- name: AddDeviceToSession :exec
INSERT INTO peer_session_devices (session_id, device_id) VALUES ($1, $2)
ON CONFLICT (session_id, device_id) DO UPDATE SET left_at = NULL;

-- name: RemoveDeviceFromSession :exec
UPDATE peer_session_devices SET left_at = NOW() WHERE session_id = $1::uuid AND device_id = $2::uuid;

-- name: ListSessionDevices :many
SELECT d.* FROM devices d
JOIN peer_session_devices psd ON d.id = psd.device_id
WHERE psd.session_id = $1::uuid AND psd.left_at IS NULL AND d.revoked_at IS NULL;

-- ─── Device Keys ────────────────────────────────────

-- name: UpsertDeviceKey :one
INSERT INTO device_keys (device_id, kex_algo, kex_public_key)
VALUES ($1::uuid, $2, $3)
ON CONFLICT (device_id) DO UPDATE
    SET kex_algo       = EXCLUDED.kex_algo,
        kex_public_key = EXCLUDED.kex_public_key,
        rotated_at     = NOW()
RETURNING *;

-- name: GetDeviceKey :one
SELECT * FROM device_keys WHERE device_id = $1::uuid;

-- name: GetDeviceKeyByNodeID :one
SELECT dk.* FROM device_keys dk
JOIN devices d ON d.id = dk.device_id
WHERE d.node_id = $1 AND d.revoked_at IS NULL AND d.is_approved = TRUE;

-- name: ListSessionDeviceKeys :many
SELECT dk.* FROM device_keys dk
JOIN peer_session_devices psd ON dk.device_id = psd.device_id
JOIN devices d ON d.id = dk.device_id
WHERE psd.session_id = $1::uuid AND psd.left_at IS NULL AND d.revoked_at IS NULL;

-- ─── Friends ────────────────────────────────────────

-- name: CreateFriendRequest :one
INSERT INTO friends (requester_id, addressee_id, status)
VALUES ($1::uuid, $2::uuid, 'pending')
RETURNING *;

-- name: GetFriendship :one
SELECT * FROM friends WHERE id = $1::uuid;

-- name: GetFriendshipBetween :one
SELECT * FROM friends
WHERE (requester_id = $1::uuid AND addressee_id = $2::uuid)
   OR (requester_id = $2::uuid AND addressee_id = $1::uuid)
LIMIT 1;

-- name: AcceptFriendRequest :exec
UPDATE friends SET status = 'accepted', updated_at = NOW()
WHERE id = $1::uuid AND addressee_id = $2::uuid AND status = 'pending';

-- name: RejectFriendRequest :exec
UPDATE friends SET status = 'rejected', updated_at = NOW()
WHERE id = $1::uuid AND addressee_id = $2::uuid AND status = 'pending';

-- name: ListAcceptedFriends :many
SELECT f.*, u.id AS friend_user_id, u.username AS friend_username, u.email AS friend_email
FROM friends f
JOIN users u ON u.id = CASE WHEN f.requester_id = $1::uuid THEN f.addressee_id ELSE f.requester_id END
WHERE (f.requester_id = $1::uuid OR f.addressee_id = $1::uuid) AND f.status = 'accepted'
ORDER BY f.updated_at DESC;

-- name: ListPendingFriendRequests :many
SELECT f.*, u.id AS friend_user_id, u.username AS friend_username, u.email AS friend_email
FROM friends f
JOIN users u ON u.id = f.requester_id
WHERE f.addressee_id = $1::uuid AND f.status = 'pending'
ORDER BY f.created_at DESC;

-- name: RemoveFriend :exec
DELETE FROM friends WHERE id = $1::uuid
AND (requester_id = $2::uuid OR addressee_id = $2::uuid);

-- name: CountPendingFriendRequests :one
SELECT COUNT(*) FROM friends WHERE addressee_id = $1::uuid AND status = 'pending';

-- name: SearchUsersByUsername :many
SELECT id, username, email FROM users
WHERE username ILIKE '%' || $1 || '%' AND id != $2::uuid
LIMIT 20;

-- name: ListDevicesByUserPublic :many
SELECT * FROM devices WHERE user_id = $1::uuid AND revoked_at IS NULL AND is_approved = TRUE AND is_public = TRUE
ORDER BY last_active DESC;

-- name: UpdateDeviceVisibility :exec
UPDATE devices SET is_public = $2 WHERE id = $1::uuid AND revoked_at IS NULL;

-- ─── Platform Stats ────────────────────────────────

-- name: CountUsers :one
SELECT COUNT(*) FROM users WHERE email_verified = TRUE;

-- name: CountDevices :one
SELECT COUNT(*) FROM devices WHERE revoked_at IS NULL AND is_approved = TRUE;

-- ─── Admin Queries ─────────────────────────────────

-- name: AdminListUsers :many
SELECT id, username, email, email_verified, created_at
FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: AdminListDevices :many
SELECT d.*, u.username AS owner_username, u.email AS owner_email
FROM devices d
JOIN users u ON u.id = d.user_id
ORDER BY d.created_at DESC LIMIT $1 OFFSET $2;

-- name: AdminListAuditLogs :many
SELECT * FROM audit_logs ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: AdminCountAuditLogs :one
SELECT COUNT(*) FROM audit_logs;

-- ─── Presence ─────────────────────────────────────

-- name: UpsertPresence :exec
INSERT INTO presence (device_id, user_id, last_seen)
VALUES ($1, $2::uuid, NOW())
ON CONFLICT (device_id) DO UPDATE SET last_seen = NOW(), user_id = EXCLUDED.user_id;

-- name: GetPresenceByUserIDs :many
SELECT DISTINCT ON (user_id) user_id, device_id, last_seen
FROM presence
WHERE user_id = ANY($1::uuid[])
ORDER BY user_id, last_seen DESC;

-- ─── Avatar ───────────────────────────────────────

-- name: UpdateUserAvatar :exec
UPDATE users SET avatar_data = $2 WHERE id = $1::uuid;

-- name: GetUserAvatar :one
SELECT avatar_data FROM users WHERE id = $1::uuid;
