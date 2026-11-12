-- name: CreateRadiusServer :one
INSERT INTO radius_servers (
    id, name, address, auth_port, accounting_port, shared_secret,
    secret_version, revision, create_time, update_time
) VALUES (
    sqlc.arg(id), sqlc.arg(name), sqlc.arg(address), sqlc.arg(auth_port), sqlc.arg(accounting_port),
    sqlc.arg(shared_secret), 1, 1, sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: GetRadiusServer :one
SELECT * FROM radius_servers WHERE id = ?;

-- name: ListRadiusServers :many
SELECT * FROM radius_servers ORDER BY name;

-- name: UpdateRadiusServer :one
-- secret_version moves only when the secret actually changes (column
-- references on the right-hand side see the old row).
UPDATE radius_servers SET
    name            = sqlc.arg(name),
    address         = sqlc.arg(address),
    auth_port       = sqlc.arg(auth_port),
    accounting_port = sqlc.arg(accounting_port),
    shared_secret   = sqlc.arg(shared_secret),
    secret_version  = CASE WHEN shared_secret = sqlc.arg(shared_secret) THEN secret_version ELSE secret_version + 1 END,
    revision        = revision + 1,
    update_time     = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND (CAST(sqlc.arg(expected_revision) AS INTEGER) = 0 OR revision = sqlc.arg(expected_revision))
RETURNING *;

-- name: DeleteRadiusServer :execrows
DELETE FROM radius_servers
WHERE id = sqlc.arg(id)
  AND (CAST(sqlc.arg(expected_revision) AS INTEGER) = 0 OR revision = sqlc.arg(expected_revision));

-- name: CountWifiNetworksUsingRadiusServer :one
SELECT COUNT(*) FROM wifi_networks WHERE radius_server_id = ?;
