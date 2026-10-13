package app

import (
	"context"
	"strings"
)

// The vulnerability count model, used by every endpoint and view:
//
//   - The unit is one open (host, CVE) finding. A CVE that hits several
//     packages of a host (or several Windows updates) counts once.
//   - Severity is the highest severity among the packages that carry the CVE.
//   - "total" counts every severity; "other" is Negligible plus Unknown.
//   - "fixable" counts CVEs that can be acted on now: the remediation is
//     upgrade, upgrade_kernel, remove_old_kernel or reboot.
//   - Only agent hosts that are not revoked are counted.

// sevRankSQL ranks a severity column; sevNameSQL maps a MAX of it back.
const sevRankSQL = `CASE %s WHEN 'Critical' THEN 5 WHEN 'High' THEN 4 WHEN 'Medium' THEN 3 WHEN 'Low' THEN 2 WHEN 'Negligible' THEN 1 ELSE 0 END`

func sevRank(col string) string { return strings.Replace(sevRankSQL, "%s", col, 1) }

// sevAggSQL is the severity of a CVE grouped over its package rows.
var sevAggSQL = `CASE MAX(` + sevRank("f.severity") + `) WHEN 5 THEN 'Critical' WHEN 4 THEN 'High' WHEN 3 THEN 'Medium' WHEN 2 THEN 'Low' WHEN 1 THEN 'Negligible' ELSE 'Unknown' END`

// vulnCounts are the open CVE counts of one host, or a sum over hosts.
type vulnCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Other    int `json:"other"` // Negligible and Unknown
	Total    int `json:"total"`
	Fixable  int `json:"fixable"`
	KEV      int `json:"kev"`
	Inactive int `json:"inactive"` // only in kernels that are not running
}

func (c *vulnCounts) add(o vulnCounts) {
	c.Critical += o.Critical
	c.High += o.High
	c.Medium += o.Medium
	c.Low += o.Low
	c.Other += o.Other
	c.Total += o.Total
	c.Fixable += o.Fixable
	c.KEV += o.KEV
	c.Inactive += o.Inactive
}

// bySeverity returns the counts keyed by lowercase severity.
func (c vulnCounts) bySeverity() map[string]int {
	return map[string]int{"critical": c.Critical, "high": c.High, "medium": c.Medium, "low": c.Low, "other": c.Other}
}

// actionable reports whether a finding can be acted on now.
func actionable(f *VulnFinding) bool {
	switch f.Remediation {
	case remUpgrade, remUpgradeK, remRemoveOld, remReboot:
		return true
	}
	return false
}

// openFindings returns one decorated finding per open CVE of a host.
func (a *App) openFindings(ctx context.Context, hostID int64) []VulnFinding {
	var out []VulnFinding
	err := a.db.SelectContext(ctx, &out, `SELECT MIN(f.id) AS id, f.tenant_id, f.host_id, f.vuln_id, GROUP_CONCAT(f.package, ', ') AS package, MIN(f.version) AS version,
		MAX(f.fixed_version) AS fixed_version, MAX(f.fix_state) AS fix_state, `+sevAggSQL+` AS severity, MAX(f.cvss) AS cvss,
		MAX(f.cvss_vector) AS cvss_vector, MAX(f.epss) AS epss, MAX(f.kev) AS kev, MAX(f.description) AS description, MIN(f.urls) AS urls,
		MIN(f.namespace) AS namespace, MIN(f.status) AS status, MIN(f.first_seen_at) AS first_seen_at, MAX(f.last_seen_at) AS last_seen_at,
		MAX(f.fixed_at) AS fixed_at, '' AS host_name FROM vuln_findings f WHERE f.host_id=? AND f.status='open' GROUP BY f.host_id, f.vuln_id`, hostID)
	if err != nil {
		return nil
	}
	a.decorateFindingsCtx(ctx, out)
	return out
}

// hostVulnCounts counts the open CVEs of a host under the model above.
func (a *App) hostVulnCounts(ctx context.Context, h *Host) vulnCounts {
	var c vulnCounts
	if h.Kind != "agent" || h.Revoked {
		return c
	}
	list := a.openFindings(ctx, h.ID)
	for i := range list {
		f := &list[i]
		c.Total++
		switch f.Severity {
		case "Critical":
			c.Critical++
		case "High":
			c.High++
		case "Medium":
			c.Medium++
		case "Low":
			c.Low++
		default:
			c.Other++
		}
		if actionable(f) {
			c.Fixable++
		}
		if f.KEV {
			c.KEV++
		}
		if f.Inactive {
			c.Inactive++
		}
	}
	return c
}

// visibleAgentHosts returns the non-revoked agent hosts the caller can see, by name.
func (a *App) visibleAgentHosts(c *Ctx) []*Host {
	tw, args := c.tenantWhere("tenant_id")
	var ids []int64
	if err := a.db.SelectContext(c.ctx(), &ids, `SELECT id FROM hosts WHERE kind='agent' AND revoked=0 AND `+tw+` ORDER BY name`, args...); err != nil {
		return nil
	}
	var out []*Host
	for _, id := range ids {
		if h, err := a.getHost(c.ctx(), id); err == nil && c.P.CanTenant(h.TenantID) {
			out = append(out, h)
		}
	}
	return out
}
