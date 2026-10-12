-- +goose Up
-- Per-host write-job policy as reported by the agent (set only in the agent's
-- local config) and the operator public keys it trusts.
ALTER TABLE hosts ADD COLUMN write_policy TEXT NOT NULL DEFAULT 'server';
ALTER TABLE hosts ADD COLUMN operator_keys TEXT NOT NULL DEFAULT '[]';
ALTER TABLE hosts ADD COLUMN operator_key_ids TEXT NOT NULL DEFAULT '[]';

-- Exact signed spec bytes for jobs that wait for an operator signature, and
-- the signature once posted.
ALTER TABLE jobs ADD COLUMN spec BLOB;
ALTER TABLE jobs ADD COLUMN operator_signature BLOB;
ALTER TABLE jobs ADD COLUMN operator_key_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE jobs DROP COLUMN operator_key_id;
ALTER TABLE jobs DROP COLUMN operator_signature;
ALTER TABLE jobs DROP COLUMN spec;
ALTER TABLE hosts DROP COLUMN operator_key_ids;
ALTER TABLE hosts DROP COLUMN operator_keys;
ALTER TABLE hosts DROP COLUMN write_policy;
