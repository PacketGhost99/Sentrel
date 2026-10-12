//go:build windows

package agent

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	pb "heartbeat/gen/heartbeatv1"
)

func runOut(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Env = os.Environ()
	return cmd.Output()
}

var (
	svcCfgMu    sync.Mutex
	svcCfgCache = map[string]svcCfg{}
	svcCfgAt    time.Time
)

type svcCfg struct {
	display string
	start   string
}

func startTypeName(t uint32, delayed bool) string {
	switch t {
	case mgr.StartAutomatic:
		if delayed {
			return "auto (delayed)"
		}
		return "auto"
	case mgr.StartManual:
		return "manual"
	case mgr.StartDisabled:
		return "disabled"
	}
	return "boot/system"
}

func stateName(s svc.State) string {
	switch s {
	case svc.Running:
		return "running"
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start_pending"
	case svc.StopPending:
		return "stop_pending"
	case svc.Paused:
		return "paused"
	}
	return "unknown"
}

// listServices returns Windows services. It opens the SCM and each service
// with read-only access so it works without administrator rights.
func listServices(ctx context.Context, all bool, watched []string) ([]*pb.Service, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, err
	}
	m := &mgr.Mgr{Handle: h}
	defer m.Disconnect()
	names, err := m.ListServices()
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, w := range watched {
		want[strings.ToLower(w)] = true
	}
	// A service is reported when all is set or it is watched by name or display name.
	selected := func(name, display string) bool {
		return all || want[strings.ToLower(name)] || want[strings.ToLower(display)]
	}
	svcCfgMu.Lock()
	if time.Since(svcCfgAt) > 10*time.Minute {
		svcCfgCache = map[string]svcCfg{}
		svcCfgAt = time.Now()
	}
	svcCfgMu.Unlock()
	var out []*pb.Service
	for _, n := range names {
		svcCfgMu.Lock()
		cfg, cached := svcCfgCache[n]
		svcCfgMu.Unlock()
		if cached && !selected(n, cfg.display) {
			continue
		}
		np, err := windows.UTF16PtrFromString(n)
		if err != nil {
			continue
		}
		sh, err := windows.OpenService(h, np, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
		if err != nil {
			continue
		}
		s := &mgr.Service{Name: n, Handle: sh}
		if !cached {
			if c, err := s.Config(); err == nil {
				cfg = svcCfg{display: c.DisplayName, start: startTypeName(c.StartType, c.DelayedAutoStart)}
				svcCfgMu.Lock()
				svcCfgCache[n] = cfg
				svcCfgMu.Unlock()
			}
			if !selected(n, cfg.display) {
				s.Close()
				continue
			}
		}
		st, err := s.Query()
		s.Close()
		if err != nil {
			continue
		}
		out = append(out, &pb.Service{Name: n, DisplayName: cfg.display, State: stateName(st.State), StartType: cfg.start})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func keyExists(path string) bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

// rebootRequired checks the standard pending-reboot registry markers.
func rebootRequired() (bool, []string) {
	var why []string
	if keyExists(`SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`) {
		why = append(why, "Component Based Servicing")
	}
	if keyExists(`SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`) {
		why = append(why, "Windows Update")
	}
	return len(why) > 0, why
}

// isAdmin reports whether the process has an elevated token.
func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(&windows.SECURITY_NT_AUTHORITY, 2, windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS, 0, 0, 0, 0, 0, 0, &sid)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)
	ok, err := windows.Token(0).IsMember(sid)
	return err == nil && ok
}
