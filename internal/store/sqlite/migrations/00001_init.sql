-- +goose Up
-- Timestamps are unix milliseconds (UTC).

CREATE TABLE devices (
    id              TEXT    NOT NULL PRIMARY KEY,
    mac             TEXT    NOT NULL UNIQUE,
    adapter         TEXT    NOT NULL,
    state           TEXT    NOT NULL CHECK (state IN ('pending', 'adopted')),
    model           TEXT    NOT NULL DEFAULT '',
    ip              TEXT    NOT NULL DEFAULT '',
    firmware        TEXT    NOT NULL DEFAULT '',
    config_revision INTEGER NOT NULL DEFAULT 0,
    last_seen       INTEGER NOT NULL,
    create_time     INTEGER NOT NULL,
    update_time     INTEGER NOT NULL
);

CREATE INDEX devices_state_adapter ON devices (state, adapter);

CREATE TABLE wifi_networks (
    id                 TEXT    NOT NULL PRIMARY KEY,
    ssid               TEXT    NOT NULL UNIQUE,
    security           TEXT    NOT NULL CHECK (security IN ('open', 'wep', 'wpa_personal', 'wpa_enterprise')),
    security_key       TEXT    NOT NULL DEFAULT '',
    band               TEXT    NOT NULL CHECK (band IN ('both', '2g', '5g')),
    network_id         TEXT    NOT NULL DEFAULT '',
    guest              BOOLEAN NOT NULL DEFAULT FALSE,
    hidden             BOOLEAN NOT NULL DEFAULT FALSE,
    default_user_group TEXT    NOT NULL DEFAULT '',
    radius_profile     TEXT    NOT NULL DEFAULT '',
    revision           INTEGER NOT NULL DEFAULT 1,
    create_time        INTEGER NOT NULL,
    update_time        INTEGER NOT NULL
);

CREATE TABLE wifi_network_ap_groups (
    wifi_network_id TEXT NOT NULL REFERENCES wifi_networks (id) ON DELETE CASCADE,
    ap_group        TEXT NOT NULL,
    PRIMARY KEY (wifi_network_id, ap_group)
);

-- +goose Down
DROP TABLE wifi_network_ap_groups;
DROP TABLE wifi_networks;
DROP INDEX devices_state_adapter;
DROP TABLE devices;
