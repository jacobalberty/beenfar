-- name: CreateSwitchPort :one
INSERT INTO switch_ports (
    id, device_id, port_index, name, disabled, native_network_id, tagging, poe_mode,
    revision, create_time, update_time
) VALUES (
    sqlc.arg(id), sqlc.arg(device_id), sqlc.arg(port_index), sqlc.arg(name), sqlc.arg(disabled),
    sqlc.arg(native_network_id), sqlc.arg(tagging), sqlc.arg(poe_mode), 1, sqlc.arg(now), sqlc.arg(now)
)
RETURNING *;

-- name: GetSwitchPort :one
SELECT * FROM switch_ports WHERE id = ?;

-- name: ListSwitchPorts :many
SELECT * FROM switch_ports
WHERE (CAST(sqlc.arg(device_id) AS TEXT) = '' OR device_id = sqlc.arg(device_id))
ORDER BY device_id, port_index;

-- name: UpdateSwitchPort :one
-- The device and port index identify the port and never change.
UPDATE switch_ports SET
    name              = sqlc.arg(name),
    disabled          = sqlc.arg(disabled),
    native_network_id = sqlc.arg(native_network_id),
    tagging           = sqlc.arg(tagging),
    poe_mode          = sqlc.arg(poe_mode),
    revision          = revision + 1,
    update_time       = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND (CAST(sqlc.arg(expected_revision) AS INTEGER) = 0 OR revision = sqlc.arg(expected_revision))
RETURNING *;

-- name: DeleteSwitchPort :execrows
DELETE FROM switch_ports
WHERE id = sqlc.arg(id)
  AND (CAST(sqlc.arg(expected_revision) AS INTEGER) = 0 OR revision = sqlc.arg(expected_revision));

-- name: ListTaggedNetworks :many
SELECT network_id FROM switch_port_tagged_networks WHERE switch_port_id = ? ORDER BY network_id;

-- name: ListAllTaggedNetworks :many
SELECT switch_port_id, network_id FROM switch_port_tagged_networks ORDER BY switch_port_id, network_id;

-- name: DeleteTaggedNetworks :exec
DELETE FROM switch_port_tagged_networks WHERE switch_port_id = ?;

-- name: InsertTaggedNetwork :exec
INSERT INTO switch_port_tagged_networks (switch_port_id, network_id) VALUES (?, ?);

-- name: CountSwitchPortsUsingNetwork :one
SELECT COUNT(*) FROM switch_ports
WHERE native_network_id = sqlc.arg(network_id)
   OR id IN (SELECT switch_port_id FROM switch_port_tagged_networks WHERE network_id = sqlc.arg(network_id));
