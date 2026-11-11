-- +goose Up
-- Per-port settings of adopted switches. A port without a row runs with the
-- device's defaults; deleting the row returns it to them. Rows go when their
-- device is forgotten.
CREATE TABLE switch_ports (
    id                TEXT    NOT NULL PRIMARY KEY,
    device_id         TEXT    NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    -- 1-based, as printed on the device.
    port_index        INTEGER NOT NULL CHECK (port_index >= 1),
    name              TEXT    NOT NULL DEFAULT '',
    disabled          BOOLEAN NOT NULL DEFAULT FALSE,
    -- Untagged network; '' means the default network.
    native_network_id TEXT    NOT NULL DEFAULT '',
    -- Which networks are carried tagged: all, none, or those listed in
    -- switch_port_tagged_networks.
    tagging           TEXT    NOT NULL CHECK (tagging IN ('all', 'none', 'custom')),
    poe_mode          TEXT    NOT NULL CHECK (poe_mode IN ('auto', 'off')),
    revision          INTEGER NOT NULL DEFAULT 1,
    create_time       INTEGER NOT NULL,
    update_time       INTEGER NOT NULL,
    UNIQUE (device_id, port_index)
);

CREATE INDEX switch_ports_native_network_id ON switch_ports (native_network_id);

-- Network references are checked by the store, as for wireless networks.
CREATE TABLE switch_port_tagged_networks (
    switch_port_id TEXT NOT NULL REFERENCES switch_ports (id) ON DELETE CASCADE,
    network_id     TEXT NOT NULL,
    PRIMARY KEY (switch_port_id, network_id)
);

CREATE INDEX switch_port_tagged_networks_network_id ON switch_port_tagged_networks (network_id);

-- +goose Down
DROP INDEX switch_port_tagged_networks_network_id;
DROP TABLE switch_port_tagged_networks;
DROP INDEX switch_ports_native_network_id;
DROP TABLE switch_ports;
