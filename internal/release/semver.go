package release

import (
	"strconv"
	"strings"
)

// Version is a parsed semantic version. Prerelease versions (-dev, -rc.1)
// sort below the release: 0.9.1-dev < 0.9.1 < 0.9.2.
type Version struct {
	Major, Minor, Patch int
	Pre                 []string
}

// ParseVersion parses "v1.2.3", "1.2.3-rc.1" and "1.2.3+build". The patch
// component may be omitted ("1.2").
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var pre string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s, pre = s[:i], s[i+1:]
		if pre == "" {
			return Version{}, false
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return Version{}, false
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return Version{}, false
		}
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return Version{}, false
		}
		n[i] = v
	}
	v := Version{Major: n[0], Minor: n[1], Patch: n[2]}
	if pre != "" {
		v.Pre = strings.Split(pre, ".")
		for _, id := range v.Pre {
			if id == "" {
				return Version{}, false
			}
		}
	}
	return v, true
}

// Compare returns -1, 0 or 1 following semver precedence.
func (a Version) Compare(b Version) int {
	for _, p := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		if c := compareIdent(a.Pre[i], b.Pre[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.Pre) < len(b.Pre):
		return -1
	case len(a.Pre) > len(b.Pre):
		return 1
	}
	return 0
}

func compareIdent(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		if an < bn {
			return -1
		} else if an > bn {
			return 1
		}
		return 0
	case aerr == nil:
		return -1 // numeric identifiers sort below alphanumeric ones
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// Newer reports whether candidate is strictly newer than current. Either
// string failing to parse yields false.
func Newer(current, candidate string) bool {
	c, ok1 := ParseVersion(current)
	n, ok2 := ParseVersion(candidate)
	return ok1 && ok2 && n.Compare(c) > 0
}

// IsPrerelease reports whether s carries a prerelease tag.
func IsPrerelease(s string) bool {
	v, ok := ParseVersion(s)
	return ok && len(v.Pre) > 0
}
