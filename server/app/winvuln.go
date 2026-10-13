package app

import (
	"context"
	"strings"

	"sentrel/server/db"

	pb "sentrel/gen/sentrelv1"
)

// nsWindowsUpdate marks findings created from pending Windows Updates. They
// have no package match: the update itself is the fix.
const nsWindowsUpdate = "windows-update"

// msrcSeverity maps a Windows Update MsrcSeverity to a finding severity.
func msrcSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return "Critical"
	case "important":
		return "High"
	case "moderate":
		return "Medium"
	case "low":
		return "Low"
	}
	return ""
}

// winPackageLabel is the short package name stored on a Windows finding: the
// update title, with the KB appended when the title does not carry it.
func winPackageLabel(p *pb.Package) string {
	t := strings.TrimSpace(p.Name)
	if t == "" {
		t = "Windows Update"
	}
	if len([]rune(t)) > 90 {
		t = string([]rune(t)[:90]) + "…"
	}
	if p.Kb != "" && !strings.Contains(strings.ToUpper(t), strings.ToUpper(p.Kb)) {
		t += " (" + p.Kb + ")"
	}
	return t
}

// ingestWindowsFindings turns the CVEs of pending Windows Updates into
// findings: one per (host, CVE, update). Findings of updates that are no
// longer pending are marked fixed. It does nothing while scanning is
// disabled or when the update search failed.
func (a *App) ingestWindowsFindings(ctx context.Context, hostID int64, inv *pb.Inventory) {
	if inv.PackageManager != "windows" || inv.Error != "" || !a.vuln.Enabled() {
		return
	}
	tenant := a.hostTenant(ctx, hostID)
	now := db.Now()
	seen := map[string]bool{}
	err := a.db.Tx(ctx, func(tx *sqlxTx) error {
		for _, p := range inv.Upgradable {
			if p.Type != "windows-update" || len(p.Cves) == 0 {
				continue
			}
			fixed := p.Kb
			if fixed == "" {
				fixed = "Windows Update"
			}
			label := winPackageLabel(p)
			sev := msrcSeverity(p.Source)
			if sev == "" {
				sev = "Unknown"
			}
			for _, cve := range p.Cves {
				seen[cve+"|"+label] = true
				if _, err := tx.ExecContext(ctx, `INSERT INTO vuln_findings (tenant_id, host_id, vuln_id, package, version, fixed_version, fix_state, severity,
					namespace, status, first_seen_at, last_seen_at) VALUES (?,?,?,?,?,?,?,?,?,'open',?,?)
					ON CONFLICT(host_id, vuln_id, package) DO UPDATE SET fixed_version=excluded.fixed_version, severity=excluded.severity,
					namespace=excluded.namespace, last_seen_at=excluded.last_seen_at,
					status=CASE WHEN vuln_findings.status='ignored' THEN 'ignored' ELSE 'open' END,
					fixed_at=CASE WHEN vuln_findings.status='ignored' THEN vuln_findings.fixed_at ELSE 0 END`,
					tenant, hostID, cve, label, "-", fixed, "fixed", sev, nsWindowsUpdate, now, now); err != nil {
					return err
				}
			}
		}
		// Findings of updates that are no longer pending are resolved.
		var open []struct {
			ID      int64  `db:"id"`
			VulnID  string `db:"vuln_id"`
			Package string `db:"package"`
		}
		if err := tx.SelectContext(ctx, &open, `SELECT id, vuln_id, package FROM vuln_findings WHERE host_id=? AND namespace=? AND status='open'`, hostID, nsWindowsUpdate); err != nil {
			return err
		}
		for _, f := range open {
			if !seen[f.VulnID+"|"+f.Package] {
				if _, err := tx.ExecContext(ctx, `UPDATE vuln_findings SET status='fixed', fixed_at=? WHERE id=?`, now, f.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		a.log.Error("windows findings", "host_id", hostID, "err", err)
		return
	}
	a.enrichWindowsFindings(ctx, hostID)
	if h, err := a.getHost(ctx, hostID); err == nil {
		a.vuln.evaluate(ctx, h)
	}
	a.events.publish(Event{Type: "vuln.scanned", TenantID: tenant, HostID: hostID})
}

// enrichWindowsFindings fills CVSS, EPSS, KEV and the description of a host's
// open Windows findings from the local vulnerability DB. Without a DB the
// findings keep their MSRC severity only.
func (a *App) enrichWindowsFindings(ctx context.Context, hostID int64) {
	var ids []string
	if err := a.db.SelectContext(ctx, &ids, `SELECT DISTINCT vuln_id FROM vuln_findings WHERE host_id=? AND namespace=? AND status<>'fixed'`, hostID, nsWindowsUpdate); err != nil || len(ids) == 0 {
		return
	}
	for _, id := range ids {
		f, ok := a.vuln.LookupCVE(id)
		if !ok {
			continue
		}
		// MSRC severity stays; the DB only fills it when the update carried none.
		sev := ""
		if f.Severity != "Unknown" {
			sev = f.Severity
		}
		_, _ = a.db.ExecContext(ctx, `UPDATE vuln_findings SET cvss=?, cvss_vector=?, epss=?, kev=?, description=?, urls=?,
			severity=CASE WHEN severity='Unknown' AND ?<>'' THEN ? ELSE severity END
			WHERE host_id=? AND vuln_id=? AND namespace=? AND status<>'fixed'`,
			f.CVSS, f.Vector, f.EPSS, f.KEV, truncateStr(f.Description, 4000), StrList(f.URLs), sev, sev, hostID, id, nsWindowsUpdate)
	}
}
