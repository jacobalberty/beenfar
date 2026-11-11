-- name: CreateAPIToken :one
INSERT INTO api_tokens (id, user_id, name, prefix, secret_hash, expire_time, create_time)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(name), sqlc.arg(prefix), sqlc.arg(secret_hash),
        sqlc.arg(expire_time), sqlc.arg(now))
RETURNING *;

-- name: GetAPIToken :one
SELECT * FROM api_tokens WHERE id = ?;

-- name: GetAPITokenByPrefix :one
SELECT * FROM api_tokens WHERE prefix = ?;

-- name: ListAPITokens :many
SELECT * FROM api_tokens
WHERE CAST(sqlc.arg(user_id) AS TEXT) = '' OR user_id = sqlc.arg(user_id)
ORDER BY create_time, id;

-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_time = sqlc.arg(now) WHERE id = sqlc.arg(id);

-- name: DeleteAPIToken :execrows
DELETE FROM api_tokens WHERE id = ?;

-- name: CreateSession :one
INSERT INTO sessions (id_hash, user_id, create_time, expire_time)
VALUES (sqlc.arg(id_hash), sqlc.arg(user_id), sqlc.arg(now), sqlc.arg(expire_time))
RETURNING *;

-- name: GetSession :one
SELECT * FROM sessions WHERE id_hash = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id_hash = ?;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expire_time <= sqlc.arg(now);
