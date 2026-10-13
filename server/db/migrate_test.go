package db

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// Migration 00010 turns the per-host permission flags into nullable
// inherit-or-override columns without losing explicit choices.
func TestHostInheritMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	x, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	sub, _ := fs.Sub(migrations, "migrations")
	p, err := goose.NewProvider(goose.DialectSQLite3, x, sub)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	// hosts: id, upgrades, ip_block, cleanup, auto_block
	seed := [][5]int{{1, 1, 1, 1, 1}, {2, 0, 1, 0, 0}, {3, 0, 0, 1, 0}, {4, 1, 0, 0, 1}}
	for _, s := range seed {
		if _, err := x.Exec(`INSERT INTO hosts (id, tenant_id, kind, name, allow_upgrades, allow_ip_block, allow_cleanup, auto_block, created_at, updated_at)
			VALUES (?,0,'agent',?,?,?,?,?,1,1)`, s[0], "h"+string(rune('0'+s[0])), s[1], s[2], s[3], s[4]); err != nil {
			t.Fatal(err)
		}
	}
	// A dependent row proves the table was not dropped and recreated.
	if _, err := x.Exec(`INSERT INTO host_groups (id, tenant_id, name, created_at) VALUES (1,0,'g',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Exec(`UPDATE hosts SET group_id=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}

	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}

	type row struct {
		up, ip, cl, ab sql.NullInt64
		name           string
		group          sql.NullInt64
	}
	got := map[int]row{}
	rs, err := x.Query(`SELECT id, name, group_id, allow_upgrades, allow_ip_block, allow_cleanup, auto_block FROM hosts`)
	if err != nil {
		t.Fatal(err)
	}
	for rs.Next() {
		var id int
		var r row
		if err := rs.Scan(&id, &r.name, &r.group, &r.up, &r.ip, &r.cl, &r.ab); err != nil {
			t.Fatal(err)
		}
		got[id] = r
	}
	rs.Close()
	if len(got) != 4 {
		t.Fatalf("rows: %d", len(got))
	}
	for _, s := range seed {
		r := got[s[0]]
		// upgrades and auto-block keep their explicit values.
		if !r.up.Valid || int(r.up.Int64) != s[1] || !r.ab.Valid || int(r.ab.Int64) != s[4] {
			t.Errorf("host %d: upgrades=%v auto_block=%v, want %d and %d", s[0], r.up, r.ab, s[1], s[4])
		}
		// IP blocking and cleanup inherit.
		if r.ip.Valid || r.cl.Valid {
			t.Errorf("host %d: ip_block=%v cleanup=%v, want NULL", s[0], r.ip, r.cl)
		}
	}
	if !got[1].group.Valid || got[1].group.Int64 != 1 {
		t.Error("group link lost")
	}
	// New rows default to inherit.
	if _, err := x.Exec(`INSERT INTO hosts (id, tenant_id, kind, name, created_at, updated_at) VALUES (9,0,'agent','new',1,1)`); err != nil {
		t.Fatal(err)
	}
	var n sql.NullInt64
	if err := x.QueryRow(`SELECT allow_upgrades FROM hosts WHERE id=9`).Scan(&n); err != nil || n.Valid {
		t.Errorf("default for new host: %v %v", n, err)
	}
	// The indexes survive.
	var idx int
	_ = x.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name IN ('hosts_tenant','hosts_group')`).Scan(&idx)
	if idx != 2 {
		t.Errorf("indexes: %d", idx)
	}
	// Down restores NOT NULL columns with the old semantics.
	if _, err := p.DownTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	var up, ip int
	if err := x.QueryRow(`SELECT allow_upgrades, allow_ip_block FROM hosts WHERE id=1`).Scan(&up, &ip); err != nil || up != 1 || ip != 0 {
		t.Errorf("down: %d %d %v", up, ip, err)
	}
}
