-- +goose Up
-- Credentials of external adapters, which call the adapter API. A token
-- names the adapter it may act as; like API tokens only a hash is kept.
CREATE TABLE adapter_tokens (
    id             TEXT    NOT NULL PRIMARY KEY,
    adapter        TEXT    NOT NULL,
    -- Public lookup part of the token.
    prefix         TEXT    NOT NULL UNIQUE,
    -- SHA-256 of the whole token, hex encoded.
    secret_hash    TEXT    NOT NULL,
    last_used_time INTEGER NOT NULL DEFAULT 0,
    create_time    INTEGER NOT NULL
);

CREATE INDEX adapter_tokens_adapter ON adapter_tokens (adapter);

-- +goose Down
DROP TABLE adapter_tokens;
