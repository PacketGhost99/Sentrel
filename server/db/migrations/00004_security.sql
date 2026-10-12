-- +goose Up
-- Replay guard for agent hellos: the last accepted hello timestamp per agent
-- survives a server restart.
CREATE TABLE agent_hello (
    agent_id   TEXT PRIMARY KEY,
    last_ms    INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE agent_hello;
