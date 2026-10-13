// Package selfupdate implements the file protocol and the privileged apply
// step of Sentrel's one-click server update.
//
// The unprivileged server downloads and verifies a release, stages it under
// <data-dir>/update/staged/ and writes request.json. A root updater (the
// currently installed binary, run by sentrel-updater.service) re-verifies the
// staged files against the trust root compiled into itself and swaps the
// binary. The server owns the staging directory, so the updater treats
// everything in it as hostile input.
package selfupdate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// File names under <data-dir>/update.
const (
	DirName     = "update"
	StagedDir   = "staged"
	RequestFile = "request.json"
	StatusFile  = "status.json"
	ResultFile  = "result.json"
	SumsName    = "SHA256SUMS"
	SigName     = "SHA256SUMS.sig"
)

// Size limits for staged files.
const (
	MaxBinaryBytes = 256 << 20
	MaxSmallBytes  = 1 << 20
)

// Updater phases, in order.
const (
	PhaseVerifying   = "verifying"
	PhaseInstalling  = "installing"
	PhaseRestarting  = "restarting"
	PhaseChecking    = "checking"
	PhaseRollingBack = "rolling_back"
)

// Result outcomes.
const (
	OutcomeSuccess    = "success"
	OutcomeRolledBack = "rolled_back"
	OutcomeFailed     = "failed" // refused before anything was changed
)

// Request is written by the server to ask the updater to apply a staged release.
type Request struct {
	Version     string   `json:"version"`
	Asset       string   `json:"asset"`
	Files       []string `json:"files"`
	RequestedBy string   `json:"requested_by"`
	RequestedAt int64    `json:"requested_at"`
}

// Status is the updater's progress, written while it runs.
type Status struct {
	Phase     string `json:"phase"`
	Version   string `json:"version"`
	UpdatedAt int64  `json:"updated_at"`
}

// Result is the updater's final record.
type Result struct {
	Outcome    string `json:"outcome"`
	From       string `json:"from"`
	To         string `json:"to"`
	Message    string `json:"message"`
	RolledBack bool   `json:"rolled_back"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
}

// State is a snapshot of the update directory as the server sees it.
type State struct {
	Request *Request
	Status  *Status
	Result  *Result
}

// Dir returns <data-dir>/update.
func Dir(dataDir string) string { return filepath.Join(dataDir, DirName) }

// StagedPath returns <data-dir>/update/staged.
func StagedPath(dataDir string) string { return filepath.Join(Dir(dataDir), StagedDir) }

func readJSON(path string, v any) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > MaxSmallBytes {
		return false
	}
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

// ReadState reads request.json, status.json and result.json (missing or
// malformed files are nil).
func ReadState(dataDir string) State {
	var s State
	d := Dir(dataDir)
	var rq Request
	if readJSON(filepath.Join(d, RequestFile), &rq) {
		s.Request = &rq
	}
	var st Status
	if readJSON(filepath.Join(d, StatusFile), &st) {
		s.Status = &st
	}
	var rs Result
	if readJSON(filepath.Join(d, ResultFile), &rs) {
		s.Result = &rs
	}
	return s
}

// Reset removes the staged files and any previous request, status and result.
// The server calls it before starting a new install.
func Reset(dataDir string) error {
	d := Dir(dataDir)
	if err := os.RemoveAll(filepath.Join(d, StagedDir)); err != nil {
		return err
	}
	for _, f := range []string{RequestFile, StatusFile, ResultFile} {
		if err := os.Remove(filepath.Join(d, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// WriteRequest atomically writes request.json (the trigger for the updater).
func WriteRequest(dataDir string, r Request) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	d := Dir(dataDir)
	tmp := filepath.Join(d, "."+RequestFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(d, RequestFile))
}
