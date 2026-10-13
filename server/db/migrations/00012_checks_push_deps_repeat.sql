-- +goose Up
-- Push (heartbeat) checks: an unguessable URL token per check and the last
-- beat received. The token is looked up by its SHA-256 hash.
ALTER TABLE checks ADD COLUMN push_token TEXT NOT NULL DEFAULT '';
ALTER TABLE checks ADD COLUMN push_token_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE checks ADD COLUMN last_beat_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE checks ADD COLUMN last_beat_up INTEGER NOT NULL DEFAULT 1;
ALTER TABLE checks ADD COLUMN last_beat_msg TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX checks_push_token_hash ON checks(push_token_hash) WHERE push_token_hash <> '';

-- Dependencies: while the parent (a host or a check in the same tenant) is
-- down, notifications for the child's alerts are withheld.
CREATE TABLE dependencies (
    child_type  TEXT NOT NULL,       -- host, check
    child_id    INTEGER NOT NULL,
    tenant_id   INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    parent_type TEXT NOT NULL,       -- host, check
    parent_id   INTEGER NOT NULL,
    PRIMARY KEY (child_type, child_id)
);
CREATE INDEX dependencies_parent ON dependencies(parent_type, parent_id);

-- +goose Down
DROP TABLE dependencies;
DROP INDEX checks_push_token_hash;
ALTER TABLE checks DROP COLUMN last_beat_msg;
ALTER TABLE checks DROP COLUMN last_beat_up;
ALTER TABLE checks DROP COLUMN last_beat_at;
ALTER TABLE checks DROP COLUMN push_token_hash;
ALTER TABLE checks DROP COLUMN push_token;
