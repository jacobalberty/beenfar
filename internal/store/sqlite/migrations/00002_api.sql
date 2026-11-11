-- +goose Up
-- Timestamps are unix milliseconds (UTC); 0 means unset.

CREATE TABLE networks (
    id           TEXT    NOT NULL PRIMARY KEY,
    name         TEXT    NOT NULL UNIQUE,
    vlan_id      INTEGER NOT NULL DEFAULT 0 CHECK (vlan_id BETWEEN 0 AND 4094),
    subnet       TEXT    NOT NULL DEFAULT '',
    dhcp_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    dhcp_start   TEXT    NOT NULL DEFAULT '',
    dhcp_stop    TEXT    NOT NULL DEFAULT '',
    domain_name  TEXT    NOT NULL DEFAULT '',
    revision     INTEGER NOT NULL DEFAULT 1,
    create_time  INTEGER NOT NULL,
    update_time  INTEGER NOT NULL
);

-- Tagged VLAN ids are unique; any number of networks may be untagged (0).
CREATE UNIQUE INDEX networks_vlan_id ON networks (vlan_id) WHERE vlan_id <> 0;

CREATE INDEX wifi_networks_network_id ON wifi_networks (network_id);

CREATE TABLE users (
    id            TEXT    NOT NULL PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE,
    -- Encoded PBKDF2 hash, see internal/auth.
    password_hash TEXT    NOT NULL,
    admin         BOOLEAN NOT NULL DEFAULT FALSE,
    revision      INTEGER NOT NULL DEFAULT 1,
    create_time   INTEGER NOT NULL,
    update_time   INTEGER NOT NULL
);

CREATE TABLE api_tokens (
    id             TEXT    NOT NULL PRIMARY KEY,
    user_id        TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name           TEXT    NOT NULL,
    -- Public lookup part of the token.
    prefix         TEXT    NOT NULL UNIQUE,
    -- SHA-256 of the whole token, hex encoded.
    secret_hash    TEXT    NOT NULL,
    expire_time    INTEGER NOT NULL DEFAULT 0,
    last_used_time INTEGER NOT NULL DEFAULT 0,
    create_time    INTEGER NOT NULL
);

CREATE INDEX api_tokens_user_id ON api_tokens (user_id);

CREATE TABLE sessions (
    -- SHA-256 of the session cookie value, hex encoded.
    id_hash     TEXT    NOT NULL PRIMARY KEY,
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    create_time INTEGER NOT NULL,
    expire_time INTEGER NOT NULL
);

CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_expire_time ON sessions (expire_time);

-- +goose Down
DROP TABLE sessions;
DROP TABLE api_tokens;
DROP TABLE users;
DROP INDEX wifi_networks_network_id;
DROP INDEX networks_vlan_id;
DROP TABLE networks;
