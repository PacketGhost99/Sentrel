//go:build windows

package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Protected DACLs: SYSTEM and Administrators get full control, nothing is
// inherited from the parent (so ProgramData's BUILTIN\Users entries do not
// apply), and children inherit the same two entries.
const (
	sddlProtectedDir  = "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	sddlProtectedFile = "D:PAI(A;;FA;;;SY)(A;;FA;;;BA)"
)

func setProtectedDACL(path, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// setOwnerSystem makes SYSTEM the owner. Best effort: it needs
// SeTakeOwnership/SeRestore, which the service account has.
func setOwnerSystem(path string) error {
	sid, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, sid, nil, nil, nil)
}

// protectDirOnly applies the protected DACL to one directory (no recursion).
func protectDirOnly(dir string) error {
	_ = setOwnerSystem(dir)
	return setProtectedDACL(dir, sddlProtectedDir)
}

// protectPaths applies the protected DACL to each directory and everything
// inside it. Links are never followed. It keeps going after an error and
// returns them joined.
func protectPaths(dirs ...string) error {
	var errs []error
	seen := map[string]bool{}
	for _, d := range dirs {
		d = filepath.Clean(d)
		if d == "." || seen[d] {
			continue
		}
		seen[d] = true
		if fi, err := os.Lstat(d); err != nil || !fi.IsDir() {
			continue
		}
		if err := protectDirOnly(d); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", d, err))
			continue
		}
		_ = filepath.WalkDir(d, func(p string, de fs.DirEntry, err error) error {
			if err != nil || p == d {
				return nil
			}
			if de.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
				return nil // junction or other reparse point: leave alone
			}
			_ = setOwnerSystem(p)
			var perr error
			if de.IsDir() {
				perr = setProtectedDACL(p, sddlProtectedDir)
			} else {
				perr = setProtectedDACL(p, sddlProtectedFile)
			}
			if perr != nil {
				errs = append(errs, fmt.Errorf("%s: %w", p, perr))
			}
			return nil
		})
	}
	return errors.Join(errs...)
}

// resetInheritedDACL replaces the explicit ACL on path with the one inherited
// from its parent folder.
func resetInheritedDACL(path string) error {
	sd, err := windows.SecurityDescriptorFromString("D:")
	if err == nil {
		var dacl *windows.ACL
		if dacl, _, err = sd.DACL(); err == nil && dacl != nil {
			err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
				windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
			if err == nil {
				return nil
			}
		}
	}
	// Fallback: icacls /reset does the same thing.
	icacls := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	if out, e := exec.Command(icacls, path, "/reset").CombinedOutput(); e != nil {
		return fmt.Errorf("reset ACL on %s: %v (icacls: %v: %s)", path, err, e, out)
	}
	return nil
}

// hardenStateDirs is run at registration and at service start.
func hardenStateDirs(stateDir, configPath string) error {
	dirs := []string{stateDir}
	// Only lock down the config folder when it is ours, never an arbitrary
	// directory the operator pointed --config at.
	if cd := filepath.Dir(configPath); configPath != "" && (strings.EqualFold(cd, filepath.Clean(stateDir)) || strings.EqualFold(cd, filepath.Dir(DefaultConfigPath()))) {
		dirs = append(dirs, cd)
	}
	return protectPaths(dirs...)
}

// stagingDir returns a fresh, SYSTEM/Administrators-only directory for
// downloaded updates, replacing any existing one.
func stagingDir(stateDir string) (string, error) {
	dir := filepath.Join(stateDir, "update-stage")
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("clear staging dir: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	if err := protectDirOnly(dir); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("protect staging dir: %w", err)
	}
	return dir, nil
}

// isStagingDir reports whether dir is the update staging directory.
func isStagingDir(stateDir, dir string) bool {
	return filepath.Clean(dir) == filepath.Join(filepath.Clean(stateDir), "update-stage")
}
