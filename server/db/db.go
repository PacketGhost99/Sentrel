// Package db opens the SQLite database and applies migrations.
//
// The pure-Go driver registered as "sqlite" comes from glebarez/go-sqlite,
// which is also what the embedded Grype vulnerability matcher uses; two
// drivers cannot both register that name, so we share it.
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB wraps sqlx.DB.
type DB struct {
	*sqlx.DB
	Path string
}

// Open opens (creating if needed) the database at dir/sentrel.db and runs
// pending migrations.
func Open(dir string) (*DB, error) {
	path := filepath.Join(dir, "sentrel.db")
	return OpenPath(path)
}

// OpenPath opens a database file at an explicit path.
func OpenPath(path string) (*DB, error) {
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(15000)" +
		"&_pragma=synchronous(NORMAL)&_txlock=immediate"
	x, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	x.SetMaxOpenConns(8)
	x.SetConnMaxIdleTime(5 * time.Minute)
	if err := x.Ping(); err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	d := &DB{DB: x, Path: path}
	if err := d.Migrate(); err != nil {
		x.Close()
		return nil, err
	}
	return d, nil
}

// Migrate applies all pending migrations.
func (d *DB) Migrate() error {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, d.DB.DB, sub)
	if err != nil {
		return err
	}
	if _, err := p.Up(context.Background()); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Tx runs fn inside a transaction.
func (d *DB) Tx(ctx context.Context, fn func(tx *sqlx.Tx) error) error {
	tx, err := d.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Now returns the current unix time; a single place so tests can reason
// about timestamps.
func Now() int64 { return time.Now().Unix() }
