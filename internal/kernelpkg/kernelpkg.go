// Package kernelpkg recognises kernel packages and their ABI. It is shared by
// the server (vulnerability remediation) and the agent (kernel removal).
//
// On Debian and Ubuntu the kernel ABI is part of the package name
// (linux-image-7.0.0-28-generic), so a newer kernel is a different package.
// On RPM distributions the kernel packages are "installonly": several
// versions are installed side by side, and the version-release is the ABI.
package kernelpkg

import (
	"regexp"
	"strings"
)

var debRe = regexp.MustCompile(`^linux-(?:image-unsigned|image|headers|modules-extra|modules|tools|cloud-tools|buildinfo)-(\d+\.\d+\.\d+-\d+)(?:-([a-z0-9][a-z0-9.+-]*))?$`)

// ABIRe is the strict form an ABI must have to be passed to a removal.
// It allows deb ABIs (7.0.0-28) and rpm version-release strings
// (5.14.0-427.13.1.el9_4).
var ABIRe = regexp.MustCompile(`^[0-9]+\.[0-9]+(?:\.[0-9]+)?-[0-9][A-Za-z0-9._]{0,63}$`)

// ValidABI reports whether s is an acceptable ABI string.
func ValidABI(s string) bool { return len(s) <= 80 && ABIRe.MatchString(s) }

var rpmNames = map[string]bool{
	"kernel": true, "kernel-core": true, "kernel-modules": true,
	"kernel-modules-core": true, "kernel-modules-extra": true, "kernel-devel": true,
}

// IsRPMName reports whether name is an installonly rpm kernel package.
func IsRPMName(name string) bool { return rpmNames[name] }

// ParseDeb returns the ABI of a deb kernel package name.
func ParseDeb(name string) (abi string, ok bool) {
	m := debRe.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// ParseRPM returns the ABI (version-release, epoch stripped) of an rpm kernel
// package.
func ParseRPM(name, version string) (abi string, ok bool) {
	if !rpmNames[name] {
		return "", false
	}
	if i := strings.IndexByte(version, ':'); i >= 0 {
		version = version[i+1:]
	}
	if version == "" {
		return "", false
	}
	return version, true
}

// Parse dispatches on the package type ("deb" or "rpm").
func Parse(pkgType, name, version string) (abi string, ok bool) {
	switch strings.ToLower(pkgType) {
	case "deb":
		return ParseDeb(name)
	case "rpm":
		return ParseRPM(name, version)
	}
	return "", false
}

// MatchesRunning reports whether abi is the running kernel. running is the
// output of uname -r (7.0.0-38-generic, 5.14.0-427.13.1.el9_4.x86_64). The ABI
// must be a prefix that ends at a "-" or "." boundary.
func MatchesRunning(abi, running string) bool {
	if abi == "" || running == "" {
		return false
	}
	if running == abi {
		return true
	}
	if !strings.HasPrefix(running, abi) {
		return false
	}
	c := running[len(abi)]
	return c == '-' || c == '.'
}

// Compare orders two ABIs: -1, 0 or 1.
func Compare(a, b string) int {
	ta, tb := tokens(a), tokens(b)
	for i := 0; i < len(ta) && i < len(tb); i++ {
		if c := cmpTok(ta[i], tb[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(ta) < len(tb):
		return -1
	case len(ta) > len(tb):
		return 1
	}
	return 0
}

func isDig(c byte) bool { return c >= '0' && c <= '9' }

// tokens splits into digit runs and letter runs, dropping separators.
func tokens(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case isDig(c):
			j := i
			for j < len(s) && isDig(s[j]) {
				j++
			}
			out = append(out, s[i:j])
			i = j
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z') {
				j++
			}
			out = append(out, s[i:j])
			i = j
		default:
			i++
		}
	}
	return out
}

func cmpTok(a, b string) int {
	da, db := isDig(a[0]), isDig(b[0])
	switch {
	case da && db:
		a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
		if len(a) != len(b) {
			if len(a) < len(b) {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	case da:
		return 1 // numbers sort after letters
	case db:
		return -1
	}
	return strings.Compare(a, b)
}

// Newest returns the highest ABI in the list ("" when empty).
func Newest(abis []string) string {
	best := ""
	for _, a := range abis {
		if best == "" || Compare(a, best) > 0 {
			best = a
		}
	}
	return best
}

// Removable returns the ABIs that may be removed: every installed ABI except
// the running one and the newest. Duplicates are collapsed; order is oldest
// first.
func Removable(installed []string, running string) []string {
	seen := map[string]bool{}
	var uniq []string
	for _, a := range installed {
		if !seen[a] {
			seen[a] = true
			uniq = append(uniq, a)
		}
	}
	newest := Newest(uniq)
	var out []string
	for _, a := range uniq {
		if a == newest || MatchesRunning(a, running) {
			continue
		}
		out = append(out, a)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && Compare(out[j-1], out[j]) > 0; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Metas are the kernel meta-packages that pull in the newest kernel.
var Metas = []string{"linux-generic", "linux-generic-hwe", "linux-image-generic", "linux-image-virtual", "linux-image-generic-hwe",
	"linux-image-amd64", "linux-image-arm64", "linux-image-cloud-amd64", "linux-image-aws", "linux-image-azure", "linux-image-gcp",
	"linux-image-oracle", "linux-image-kvm", "linux-image-lowlatency", "linux-virtual", "linux-aws", "linux-azure", "linux-gcp",
	"linux-oracle", "linux-kvm", "linux-lowlatency", "linux-cloud-amd64", "kernel"}

// IsMeta reports whether name is a kernel meta-package.
func IsMeta(name string) bool {
	for _, m := range Metas {
		if m == name {
			return true
		}
	}
	return false
}
