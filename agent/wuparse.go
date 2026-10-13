package agent

import (
	"regexp"
	"sort"
	"strings"

	pb "sentrel/gen/sentrelv1"
)

// wuInfo is the plain-data view of one IUpdate, copied out of COM so the
// mapping to an inventory package can be tested on any platform.
type wuInfo struct {
	Title      string
	MsrcSev    string
	KBs        []string // KBArticleIDs, with or without a "KB" prefix
	CVEs       []string // CveIDs
	Categories []string
}

var cveRe = regexp.MustCompile(`^CVE-\d{4}-\d{4,}$`)

// normCVEs upper-cases, validates and de-duplicates CVE ids, sorted.
func normCVEs(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range in {
		c = strings.ToUpper(strings.TrimSpace(c))
		if cveRe.MatchString(c) && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// packageFromUpdate maps a Windows Update to an inventory package: Source
// carries the MSRC severity, Kb the first KB article and Cves the CVE ids.
func packageFromUpdate(u wuInfo) *pb.Package {
	p := &pb.Package{Type: "windows-update", Version: "-", Name: strings.TrimSpace(u.Title), Cves: normCVEs(u.CVEs)}
	for _, k := range u.KBs {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(k), "KB") {
			k = "KB" + k
		}
		p.Kb = strings.ToUpper(k)
		break
	}
	p.Source = strings.TrimSpace(u.MsrcSev)
	for _, n := range u.Categories {
		if n == "Security Updates" || n == "Critical Updates" {
			p.Security = true
		}
		if p.Publisher == "" {
			p.Publisher = n
		}
	}
	p.NewVersion = p.Kb
	if p.NewVersion == "" {
		p.NewVersion = "available"
	}
	return p
}
