//go:build linux

package agent

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
)

// runningKernel returns uname -r.
func runningKernel() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	return cmd.Output()
}

func realKernelDeps() kernelDeps {
	mgr := packageManager()
	return kernelDeps{run: execRunner, mgr: mgr, running: runningKernel(),
		exec: func(ctx context.Context, w io.Writer, env []string, name string, args ...string) error {
			return runLogged(ctx, w, env, name, args...)
		}}
}

// privRemoveKernel implements `remove-kernel [--dry-run] ABI`.
func privRemoveKernel(ctx context.Context, w io.Writer, args []string) error {
	dry, abi, err := parseRemoveKernelArgs(args)
	if err != nil {
		return err
	}
	return removeKernel(ctx, w, realKernelDeps(), dry, abi)
}
