package agentdist

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// ManifestName is the manifest file written next to the packages by
// scripts/build.sh. Packages live in the "pkg" subdirectory.
const ManifestName = "packages.json"

// Package is one installable agent package (deb, rpm, msi).
type Package struct {
	File   string `json:"file"`
	OS     string `json:"os"`     // linux, windows
	Arch   string `json:"arch"`   // amd64, arm64
	Format string `json:"format"` // deb, rpm, msi, binary
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ErrNoPackage means the file is not in the manifest.
var ErrNoPackage = errors.New("package not found")

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~-]{0,127}$`)

// loadManifest reads packages.json from fsys, dropping malformed entries and
// entries whose file is missing or whose size differs from the manifest.
func loadManifest(fsys fs.FS, sub string) []Package {
	raw, err := fs.ReadFile(fsys, path.Join(sub, ManifestName))
	if err != nil {
		return nil
	}
	var all []Package
	if json.Unmarshal(raw, &all) != nil {
		return nil
	}
	var out []Package
	for _, p := range all {
		if !safeName.MatchString(p.File) || p.File == ManifestName {
			continue
		}
		fi, err := fs.Stat(fsys, path.Join(sub, "pkg", p.File))
		if err != nil || fi.IsDir() || fi.Size() != p.Size {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Packages lists the agent packages available, preferring dir when it holds
// a manifest, otherwise the ones embedded in the server.
func Packages(dir string) []Package {
	if dir != "" {
		if ps := loadManifest(os.DirFS(dir), "."); ps != nil {
			return ps
		}
	}
	sub, err := fs.Sub(embedded, "bin")
	if err != nil {
		return nil
	}
	return loadManifest(sub, ".")
}

// OpenPackage opens a package by file name. Only names in the manifest are
// served: anything else, including path separators, is ErrNoPackage.
func OpenPackage(dir, file string) (*Package, io.ReadCloser, error) {
	if !safeName.MatchString(file) {
		return nil, nil, ErrNoPackage
	}
	for _, p := range Packages(dir) {
		if p.File != file {
			continue
		}
		p := p
		if dir != "" && loadManifest(os.DirFS(dir), ".") != nil {
			f, err := os.Open(filepath.Join(dir, "pkg", p.File))
			if err != nil {
				return nil, nil, ErrNoPackage
			}
			return &p, f, nil
		}
		data, err := embedded.ReadFile("bin/pkg/" + p.File)
		if err != nil {
			return nil, nil, ErrNoPackage
		}
		return &p, io.NopCloser(bytes.NewReader(data)), nil
	}
	return nil, nil, ErrNoPackage
}

// Version returns the agent version the server offers: the VERSION file of
// dir when it has one, otherwise the embedded build's. "unknown" if neither.
func Version(dir string) string {
	if dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, "VERSION")); err == nil {
			if v := strings.TrimSpace(string(b)); v != "" {
				return v
			}
		}
	}
	sub, err := fs.Sub(embedded, "bin")
	if err != nil {
		return "unknown"
	}
	return readVersion(sub)
}
