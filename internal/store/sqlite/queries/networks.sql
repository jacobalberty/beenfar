-- name: CreateNetwork :one
INSERT INTO networks (
    id, name, vlan_id, subnet, dhcp_enabled, dhcp_start, dhcp_stop, domain_name,
    revision, create_time, update_time
) VALUES (
    sqlc.arg(id), sqlc.arg(name), sqlc.arg(vlan_id), sqlc.arg(subnet), sqlc.arg(dhcp_enabled),
    sqlc.arg(dhcp_start), sqlc.arg(dhcp_stop), sqlc.arg(domain_name), 1, sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: GetNetwork :one
SELECT * FROM networks WHERE id = ?;

-- name: ListNetworks :many
SELECT * FROM networks ORDER BY name;

-- name: UpdateNetwork :one
UPDATE networks SET
    name         = sqlc.arg(name),
    vlan_id      = sqlc.arg(vlan_id),
    subnet       = sqlc.arg(subnet),
    dhcp_enabled = sqlc.arg(dhcp_enabled),
    dhcp_start   = sqlc.arg(dhcp_start),
    dhcp_stop    = sqlc.arg(dhcp_stop),
    domain_name  = sqlc.arg(domain_name),
    revision     = revision + 1,
    update_time  = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND (CAST(sqlc.arg(expected_revision) AS INTEGER) = 0 OR revision = sqlc.arg(expected_revision))
RETURNING *;

-- name: DeleteNetwork :execrows
DELETE FROM networks
WHERE id = sqlc.arg(id)
  AND (CAST(sqlc.arg(expected_revision) AS INTEGER) = 0 OR revision = sqlc.arg(expected_revision));

-- name: CountWifiNetworksUsingNetwork :one
SELECT COUNT(*) FROM wifi_networks WHERE network_id = ?;
