package app

import (
	"context"
	"database/sql"
	"fmt"
)

// A dependency names a parent (a host or a check in the same tenant) for a
// host or a check. While the parent is down, alerts for the dependent are
// still raised and shown, but their notifications are withheld.

// DepParent is the parent of a host or check as shown in the API.
type DepParent struct {
	Type string `json:"type"` // host, check
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Down bool   `json:"down"`
}

type depRow struct {
	ChildType  string `db:"child_type"`
	ChildID    int64  `db:"child_id"`
	TenantID   int64  `db:"tenant_id"`
	ParentType string `db:"parent_type"`
	ParentID   int64  `db:"parent_id"`
}

func validDepType(t string) bool { return t == "host" || t == "check" }

// depOf returns the stored dependency of a child, if any.
func (a *App) depOf(ctx context.Context, childType string, childID int64) *depRow {
	var r depRow
	if err := a.db.GetContext(ctx, &r, `SELECT * FROM dependencies WHERE child_type=? AND child_id=?`, childType, childID); err != nil {
		return nil
	}
	return &r
}

// depState reports the name, tenant and down state of a host or check.
func (a *App) depState(ctx context.Context, typ string, id int64) (name string, tenant int64, down, ok bool) {
	switch typ {
	case "host":
		var h struct {
			Name     string `db:"name"`
			TenantID int64  `db:"tenant_id"`
			Status   string `db:"status"`
		}
		if err := a.db.GetContext(ctx, &h, `SELECT name, tenant_id, status FROM hosts WHERE id=?`, id); err != nil {
			return "", 0, false, false
		}
		return h.Name, h.TenantID, h.Status == "down", true
	case "check":
		var k struct {
			Name     string `db:"name"`
			TenantID int64  `db:"tenant_id"`
			Status   string `db:"status"`
			Fails    int    `db:"consecutive_failures"`
			Thresh   int    `db:"failure_threshold"`
			Enabled  bool   `db:"enabled"`
		}
		if err := a.db.GetContext(ctx, &k, `SELECT name, tenant_id, status, consecutive_failures, failure_threshold, enabled FROM checks WHERE id=?`, id); err != nil {
			return "", 0, false, false
		}
		return k.Name, k.TenantID, k.Enabled && k.Status == "down" && k.Fails >= k.Thresh, true
	}
	return "", 0, false, false
}

// parentInfo describes the parent of a host or check for the API, or nil.
func (a *App) parentInfo(ctx context.Context, childType string, childID int64) *DepParent {
	d := a.depOf(ctx, childType, childID)
	if d == nil {
		return nil
	}
	name, _, down, ok := a.depState(ctx, d.ParentType, d.ParentID)
	if !ok {
		return nil
	}
	return &DepParent{Type: d.ParentType, ID: d.ParentID, Name: name, Down: down}
}

// validateDependency checks a proposed parent for a child. An empty parent
// type means "no parent" and is always valid.
func (a *App) validateDependency(ctx context.Context, tenant int64, childType string, childID int64, parentType string, parentID int64) error {
	if parentType == "" && parentID == 0 {
		return nil
	}
	if !validDepType(childType) || !validDepType(parentType) || parentID <= 0 {
		return badRequest("parent must be a host or a check")
	}
	if parentType == childType && parentID == childID {
		return badRequest("something cannot depend on itself")
	}
	_, pt, _, ok := a.depState(ctx, parentType, parentID)
	if !ok || pt != tenant {
		// Same wording for "missing" and "other tenant" so ids do not leak.
		return badRequest("unknown parent")
	}
	// A host cannot depend on one of its own checks: that check's alerts
	// would suppress the host's, which in turn feed the check.
	if childType == "host" && parentType == "check" {
		var hid sql.NullInt64
		if err := a.db.GetContext(ctx, &hid, `SELECT host_id FROM checks WHERE id=?`, parentID); err == nil && hid.Valid && hid.Int64 == childID {
			return badRequest("a host cannot depend on its own check")
		}
	}
	// Walk up from the proposed parent; reaching the child means a cycle.
	curT, curID := parentType, parentID
	for i := 0; i < 64; i++ {
		if curT == childType && curID == childID {
			return badRequest("this dependency would create a cycle")
		}
		d := a.depOf(ctx, curT, curID)
		if d == nil {
			return nil
		}
		curT, curID = d.ParentType, d.ParentID
	}
	return badRequest("dependency chain is too deep")
}

// setDependency stores or clears (empty parent type) the parent of a child.
// Callers validate first.
func (a *App) setDependency(ctx context.Context, tenant int64, childType string, childID int64, parentType string, parentID int64) error {
	if parentType == "" {
		_, err := a.db.ExecContext(ctx, `DELETE FROM dependencies WHERE child_type=? AND child_id=?`, childType, childID)
		return err
	}
	_, err := a.db.ExecContext(ctx, `INSERT INTO dependencies (child_type, child_id, tenant_id, parent_type, parent_id) VALUES (?,?,?,?,?)
		ON CONFLICT(child_type, child_id) DO UPDATE SET tenant_id=excluded.tenant_id, parent_type=excluded.parent_type, parent_id=excluded.parent_id`,
		childType, childID, tenant, parentType, parentID)
	return err
}

// setDependencyTx is setDependency inside a transaction.
func setDependencyTx(ctx context.Context, tx *sqlxTx, tenant int64, childType string, childID int64, parentType string, parentID int64) error {
	if parentType == "" {
		_, err := tx.ExecContext(ctx, `DELETE FROM dependencies WHERE child_type=? AND child_id=?`, childType, childID)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO dependencies (child_type, child_id, tenant_id, parent_type, parent_id) VALUES (?,?,?,?,?)
		ON CONFLICT(child_type, child_id) DO UPDATE SET tenant_id=excluded.tenant_id, parent_type=excluded.parent_type, parent_id=excluded.parent_id`,
		childType, childID, tenant, parentType, parentID)
	return err
}

// forgetDependencies removes every dependency that involves an object, as
// child or parent. For a host it also covers the host's checks.
func (a *App) forgetDependencies(ctx context.Context, typ string, id int64) {
	del := func(t string, i int64) {
		_, _ = a.db.ExecContext(ctx, `DELETE FROM dependencies WHERE (child_type=? AND child_id=?) OR (parent_type=? AND parent_id=?)`, t, i, t, i)
	}
	if typ == "host" {
		var ids []int64
		_ = a.db.SelectContext(ctx, &ids, `SELECT id FROM checks WHERE host_id=?`, id)
		for _, k := range ids {
			del("check", k)
		}
	}
	del(typ, id)
}

// dependencyReason returns "parent X is down" when an alert's check or host
// depends on a parent that is down, else "".
func (a *App) dependencyReason(al *Alert) string {
	ctx := context.Background()
	// One effective parent: the check's own dependency if it has one, otherwise
	// the host's. An explicit parent replaces inheritance rather than adding to it.
	var d *depRow
	if al.CheckID != nil {
		d = a.depOf(ctx, "check", *al.CheckID)
	}
	if d == nil && al.HostID != nil {
		d = a.depOf(ctx, "host", *al.HostID)
		// A check's own alert is never silenced by its host's inherited parent
		// being that same check.
		if d != nil && al.CheckID != nil && d.ParentType == "check" && d.ParentID == *al.CheckID {
			d = nil
		}
	}
	if d == nil {
		return ""
	}
	if name, _, down, ok := a.depState(ctx, d.ParentType, d.ParentID); ok && down {
		return fmt.Sprintf("parent %s is down", name)
	}
	return ""
}

// --- API helpers ---

// depRequest is the optional parent in host and check update bodies. A nil
// ParentType leaves the dependency unchanged; "" clears it.
type depRequest struct {
	ParentType *string `json:"parent_type"`
	ParentID   *int64  `json:"parent_id"`
}

func (d depRequest) present() bool { return d.ParentType != nil }

func (d depRequest) values() (string, int64) {
	var id int64
	if d.ParentID != nil {
		id = *d.ParentID
	}
	if d.ParentType == nil || *d.ParentType == "" {
		return "", 0
	}
	return *d.ParentType, id
}

// auditDep records a dependency change.
func (c *Ctx) auditDep(tenant int64, childType string, childID int64, pt string, pid int64) {
	c.auditT(tenant, "dependency.updated", childType, itoa(childID), map[string]any{"parent_type": pt, "parent_id": pid})
}
