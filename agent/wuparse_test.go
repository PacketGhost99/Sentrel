package agent

import (
	"reflect"
	"testing"
)

func TestPackageFromUpdate(t *testing.T) {
	p := packageFromUpdate(wuInfo{
		Title:      " 2026-10 Cumulative Update for Windows 11 (KB5044284) ",
		MsrcSev:    "Critical",
		KBs:        []string{"5044284", "KB999"},
		CVEs:       []string{"cve-2026-0002", "CVE-2026-0001", "CVE-2026-0001", "bogus", ""},
		Categories: []string{"Security Updates", "Windows 11"},
	})
	if p.Kb != "KB5044284" || p.NewVersion != "KB5044284" || p.Source != "Critical" || !p.Security || p.Type != "windows-update" {
		t.Fatalf("unexpected package %+v", p)
	}
	if want := []string{"CVE-2026-0001", "CVE-2026-0002"}; !reflect.DeepEqual(p.Cves, want) {
		t.Fatalf("cves = %v", p.Cves)
	}
	if p.Name != "2026-10 Cumulative Update for Windows 11 (KB5044284)" || p.Publisher != "Security Updates" {
		t.Fatalf("name/publisher: %q %q", p.Name, p.Publisher)
	}
	q := packageFromUpdate(wuInfo{Title: "Driver update", Categories: []string{"Drivers"}})
	if q.Kb != "" || q.NewVersion != "available" || q.Security || len(q.Cves) != 0 {
		t.Fatalf("unexpected %+v", q)
	}
}
