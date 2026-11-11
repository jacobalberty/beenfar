-- +goose Up
-- Where an account comes from. External (proxy, oidc) users have an empty
-- password_hash and cannot sign in with a password.
ALTER TABLE users ADD COLUMN source TEXT NOT NULL DEFAULT 'local'
    CHECK (source IN ('local', 'proxy', 'oidc'));

-- How a session was started: '' for password login, 'oidc:<provider id>' for
-- OIDC (used for provider sign-out).
ALTER TABLE sessions ADD COLUMN provider TEXT NOT NULL DEFAULT '';

-- Links an external identity to a beenfar user. The subject is the
-- provider's stable id (OIDC "sub", or the proxy's user header), so a rename
-- upstream does not create a second user.
CREATE TABLE user_identities (
    provider    TEXT    NOT NULL, -- 'proxy' or 'oidc:<provider id>'
    subject     TEXT    NOT NULL,
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    create_time INTEGER NOT NULL,
    PRIMARY KEY (provider, subject)
);

CREATE INDEX user_identities_user_id ON user_identities (user_id);

-- +goose Down
DROP TABLE user_identities;
ALTER TABLE sessions DROP COLUMN provider;
ALTER TABLE users DROP COLUMN source;
