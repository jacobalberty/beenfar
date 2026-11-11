-- +goose Up
-- RADIUS servers that WPA-enterprise wireless networks authenticate against.
-- The shared secret is stored so it can be sent to devices, but the API never
-- returns it; secret_version counts its changes instead, so clients can tell
-- that it was replaced without seeing it.
CREATE TABLE radius_servers (
    id              TEXT    NOT NULL PRIMARY KEY,
    name            TEXT    NOT NULL UNIQUE,
    address         TEXT    NOT NULL,
    auth_port       INTEGER NOT NULL CHECK (auth_port BETWEEN 1 AND 65535),
    -- 0 means no accounting.
    accounting_port INTEGER NOT NULL DEFAULT 0 CHECK (accounting_port BETWEEN 0 AND 65535),
    shared_secret   TEXT    NOT NULL CHECK (shared_secret <> ''),
    secret_version  INTEGER NOT NULL DEFAULT 1,
    revision        INTEGER NOT NULL DEFAULT 1,
    create_time     INTEGER NOT NULL,
    update_time     INTEGER NOT NULL
);

-- Like network_id, the reference is checked by the store rather than a
-- foreign key, so that '' can mean "none" and a delete can report which
-- wireless networks still use the server.
ALTER TABLE wifi_networks ADD COLUMN radius_server_id TEXT NOT NULL DEFAULT '';
CREATE INDEX wifi_networks_radius_server_id ON wifi_networks (radius_server_id);

-- +goose Down
DROP INDEX wifi_networks_radius_server_id;
ALTER TABLE wifi_networks DROP COLUMN radius_server_id;
DROP TABLE radius_servers;
