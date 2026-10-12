//go:build linux

package agent

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	pb "heartbeat/gen/heartbeatv1"
)

func runOut(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	return cmd.Output()
}

// listServices returns systemd services: all of them when all is true,
// otherwise only the watched ones.
func listServices(ctx context.Context, all bool, watched []string) ([]*pb.Service, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil, nil
	}
	out, err := runOut(ctx, 20*time.Second, "systemctl", "list-units", "--type=service", "--all", "--no-legend", "--no-pager", "--plain")
	if err != nil && len(out) == 0 {
		return nil, err
	}
	enabled := map[string]string{}
	if uf, err := runOut(ctx, 20*time.Second, "systemctl", "list-unit-files", "--type=service", "--no-legend", "--no-pager"); err == nil {
		s := bufio.NewScanner(bytes.NewReader(uf))
		for s.Scan() {
			f := strings.Fields(s.Text())
			if len(f) >= 2 {
				enabled[f[0]] = f[1]
			}
		}
	}
	want := map[string]bool{}
	for _, w := range watched {
		want[strings.TrimSuffix(strings.ToLower(w), ".service")] = true
	}
	var svcs []*pb.Service
	s := bufio.NewScanner(bytes.NewReader(out))
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) < 4 {
			continue
		}
		name := f[0]
		short := strings.TrimSuffix(strings.ToLower(name), ".service")
		if !all && !want[short] {
			continue
		}
		// Skip template instances of noisy units when listing everything.
		if all && f[1] == "not-found" && !want[short] {
			continue
		}
		desc := ""
		if len(f) > 4 {
			desc = strings.Join(f[4:], " ")
		}
		svcs = append(svcs, &pb.Service{Name: strings.TrimSuffix(name, ".service"), DisplayName: desc, State: f[2], SubState: f[3], StartType: enabled[name]})
	}
	sort.Slice(svcs, func(i, j int) bool { return svcs[i].Name < svcs[j].Name })
	return svcs, nil
}

// rebootRequired checks Debian/Ubuntu's marker and RHEL's needs-restarting.
func rebootRequired() (bool, []string) {
	if _, err := os.Stat("/var/run/reboot-required"); err == nil {
		var pkgs []string
		if b, err := os.ReadFile("/var/run/reboot-required.pkgs"); err == nil {
			for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				if l != "" {
					pkgs = append(pkgs, l)
				}
			}
		}
		return true, pkgs
	}
	if p, err := exec.LookPath("needs-restarting"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, p, "-r")
		if err := cmd.Run(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
				return true, nil
			}
		}
	}
	return false, nil
}
