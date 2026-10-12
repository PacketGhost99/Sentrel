//go:build !windows

package agent

// hardenStateDirs is a no-op off Windows (state is 0700 and owned by the
// agent user).
func hardenStateDirs(stateDir, configPath string) error { return nil }

// stagingDir returns where downloaded updates are written. On Linux the root
// helper requires the file directly inside the state directory.
func stagingDir(stateDir string) (string, error) { return stateDir, nil }
