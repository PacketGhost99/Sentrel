-- +goose Up
-- Remote script execution was removed; drop its per-host opt-in flag.
ALTER TABLE hosts DROP COLUMN allow_scripts;

-- +goose Down
ALTER TABLE hosts ADD COLUMN allow_scripts INTEGER NOT NULL DEFAULT 0;
