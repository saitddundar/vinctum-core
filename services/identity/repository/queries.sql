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
