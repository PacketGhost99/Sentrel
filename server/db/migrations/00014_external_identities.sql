-- +goose Up
-- Stable identities for single sign-on users. OIDC users are keyed by issuer and
-- subject; forward-auth users by the configured header name and the header
-- value. Usernames, emails and display names are mutable attributes.
CREATE TABLE external_identities (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider    TEXT NOT NULL,
    issuer      TEXT NOT NULL,
    subject     TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    UNIQUE (provider, issuer, subject)
);
CREATE INDEX idx_external_identities_user ON external_identities(user_id);

-- +goose Down
DROP TABLE external_identities;
