-- name: CreateWifiNetwork :one
INSERT INTO wifi_networks (
    id, ssid, security, security_key, band, network_id, guest, hidden,
    default_user_group, radius_profile, revision, create_time, update_time
) VALUES (
    sqlc.arg(id), sqlc.arg(ssid), sqlc.arg(security), sqlc.arg(security_key), sqlc.arg(band),
    sqlc.arg(network_id), sqlc.arg(guest), sqlc.arg(hidden), sqlc.arg(default_user_group),
    sqlc.arg(radius_profile), 1, sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: GetWifiNetwork :one
SELECT * FROM wifi_networks WHERE id = ?;

-- name: ListWifiNetworks :many
SELECT * FROM wifi_networks ORDER BY ssid;

-- name: UpdateWifiNetwork :one
UPDATE wifi_networks SET
    ssid               = sqlc.arg(ssid),
    security           = sqlc.arg(security),
    security_key       = sqlc.arg(security_key),
    band               = sqlc.arg(band),
    network_id         = sqlc.arg(network_id),
    guest              = sqlc.arg(guest),
    hidden             = sqlc.arg(hidden),
    default_user_group = sqlc.arg(default_user_group),
    radius_profile     = sqlc.arg(radius_profile),
    revision           = revision + 1,
    update_time        = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND (CAST(sqlc.arg(expected_revision) AS INTEGER) = 0 OR revision = sqlc.arg(expected_revision))
RETURNING *;

-- name: DeleteWifiNetwork :execrows
DELETE FROM wifi_networks
WHERE id = sqlc.arg(id)
  AND (CAST(sqlc.arg(expected_revision) AS INTEGER) = 0 OR revision = sqlc.arg(expected_revision));

-- name: ListAPGroups :many
SELECT ap_group FROM wifi_network_ap_groups WHERE wifi_network_id = ? ORDER BY ap_group;

-- name: ListAllAPGroups :many
SELECT wifi_network_id, ap_group FROM wifi_network_ap_groups ORDER BY wifi_network_id, ap_group;

-- name: DeleteAPGroups :exec
DELETE FROM wifi_network_ap_groups WHERE wifi_network_id = ?;

-- name: InsertAPGroup :exec
INSERT INTO wifi_network_ap_groups (wifi_network_id, ap_group) VALUES (?, ?);
