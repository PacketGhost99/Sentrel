-- +goose Up
-- Per-host security and permission flags become nullable: NULL inherits the
-- global default (Settings -> Agents), a value overrides it.
-- Columns are replaced in place (add nullable, copy, drop, rename) instead of
-- rebuilding the hosts table, so foreign keys and indexes are untouched.
ALTER TABLE hosts ADD COLUMN allow_upgrades_n INTEGER;
ALTER TABLE hosts ADD COLUMN allow_ip_block_n INTEGER;
ALTER TABLE hosts ADD COLUMN allow_cleanup_n INTEGER;
ALTER TABLE hosts ADD COLUMN auto_block_n INTEGER;
-- Explicit choices are kept for upgrades and auto-block. IP blocking and
-- cleanup move to inherit so existing hosts pick up the new global defaults.
UPDATE hosts SET allow_upgrades_n = allow_upgrades, auto_block_n = auto_block;
ALTER TABLE hosts DROP COLUMN allow_upgrades;
ALTER TABLE hosts DROP COLUMN allow_ip_block;
ALTER TABLE hosts DROP COLUMN allow_cleanup;
ALTER TABLE hosts DROP COLUMN auto_block;
ALTER TABLE hosts RENAME COLUMN allow_upgrades_n TO allow_upgrades;
ALTER TABLE hosts RENAME COLUMN allow_ip_block_n TO allow_ip_block;
ALTER TABLE hosts RENAME COLUMN allow_cleanup_n TO allow_cleanup;
ALTER TABLE hosts RENAME COLUMN auto_block_n TO auto_block;

-- +goose Down
ALTER TABLE hosts ADD COLUMN allow_upgrades_o INTEGER NOT NULL DEFAULT 0;
ALTER TABLE hosts ADD COLUMN allow_ip_block_o INTEGER NOT NULL DEFAULT 0;
ALTER TABLE hosts ADD COLUMN allow_cleanup_o INTEGER NOT NULL DEFAULT 0;
ALTER TABLE hosts ADD COLUMN auto_block_o INTEGER NOT NULL DEFAULT 0;
UPDATE hosts SET allow_upgrades_o = COALESCE(allow_upgrades, 0), allow_ip_block_o = COALESCE(allow_ip_block, 0),
  allow_cleanup_o = COALESCE(allow_cleanup, 0), auto_block_o = COALESCE(auto_block, 0);
ALTER TABLE hosts DROP COLUMN allow_upgrades;
ALTER TABLE hosts DROP COLUMN allow_ip_block;
ALTER TABLE hosts DROP COLUMN allow_cleanup;
ALTER TABLE hosts DROP COLUMN auto_block;
ALTER TABLE hosts RENAME COLUMN allow_upgrades_o TO allow_upgrades;
ALTER TABLE hosts RENAME COLUMN allow_ip_block_o TO allow_ip_block;
ALTER TABLE hosts RENAME COLUMN allow_cleanup_o TO allow_cleanup;
ALTER TABLE hosts RENAME COLUMN auto_block_o TO auto_block;
