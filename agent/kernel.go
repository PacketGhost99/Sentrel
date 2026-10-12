package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"sentrel/internal/kernelpkg"
)

// cmdRunner runs a read-only command and returns its stdout. It is injected so
// the removal logic can be tested without a package manager.
type cmdRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// kernelDeps bundles everything the kernel removal logic touches.
type kernelDeps struct {
	run     cmdRunner
	mgr     string // "apt", "dnf" or "yum"
	running string // uname -r
	// exec runs a state-changing command, streaming output to w.
	exec func(ctx context.Context, w io.Writer, env []string, name string, args ...string) error
}

type kernelPkg struct {
	Name     string // package name
	Spec     string // what the package manager is given (name, or NEVRA for rpm)
	ABI      string
	SizeByte int64
}

// removalPlan is the exact set of packages that belong to one kernel ABI.
type removalPlan struct {
	ABI      string
	Packages []string // exact names / NEVRAs, sorted
	Bytes    int64
}

// installedKernelPkgs lists every installed kernel package.
func installedKernelPkgs(ctx context.Context, d kernelDeps) ([]kernelPkg, error) {
	var out []kernelPkg
	switch d.mgr {
	case "apt":
		b, err := d.run(ctx, "dpkg-query", "-W", "-f", "${db:Status-Abbrev}\t${binary:Package}\t${Version}\t${Installed-Size}\n")
		if err != nil {
			return nil, fmt.Errorf("dpkg-query: %w", err)
		}
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Split(l, "\t")
			if len(f) < 4 || !strings.HasPrefix(f[0], "ii") {
				continue
			}
			name := f[1]
			if i := strings.IndexByte(name, ':'); i > 0 {
				name = name[:i]
			}
			abi, ok := kernelpkg.ParseDeb(name)
			if !ok || !pkgNameRe.MatchString(name) {
				continue
			}
			kib, _ := strconv.ParseInt(strings.TrimSpace(f[3]), 10, 64)
			out = append(out, kernelPkg{Name: name, Spec: name, ABI: abi, SizeByte: kib * 1024})
		}
	case "dnf", "yum":
		b, err := d.run(ctx, "rpm", "-qa", "--qf", "%{NAME}\t%{EPOCHNUM}\t%{VERSION}-%{RELEASE}\t%{ARCH}\t%{SIZE}\n")
		if err != nil {
			return nil, fmt.Errorf("rpm -qa: %w", err)
		}
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Split(l, "\t")
			if len(f) < 5 {
				continue
			}
			abi, ok := kernelpkg.ParseRPM(f[0], f[2])
			if !ok {
				continue
			}
			spec := f[0] + "-" + f[2] + "." + f[3]
			if f[1] != "0" && f[1] != "" {
				spec = f[0] + "-" + f[1] + ":" + f[2] + "." + f[3]
			}
			if !pkgNameRe.MatchString(spec) {
				continue
			}
			sz, _ := strconv.ParseInt(strings.TrimSpace(f[4]), 10, 64)
			out = append(out, kernelPkg{Name: f[0], Spec: spec, ABI: abi, SizeByte: sz})
		}
	default:
		return nil, fmt.Errorf("kernel removal is not supported with package manager %q", d.mgr)
	}
	return out, nil
}

func abiList(pk []kernelPkg) []string {
	var l []string
	for _, p := range pk {
		l = append(l, p.ABI)
	}
	return l
}

// planKernelRemoval validates abi and returns the exact packages to remove. It
// refuses the running kernel and the newest installed kernel.
func planKernelRemoval(ctx context.Context, d kernelDeps, abi string) (*removalPlan, error) {
	if !kernelpkg.ValidABI(abi) {
		return nil, fmt.Errorf("invalid kernel ABI %q", abi)
	}
	pk, err := installedKernelPkgs(ctx, d)
	if err != nil {
		return nil, err
	}
	return planFrom(pk, d.running, abi)
}

func planFrom(pk []kernelPkg, running, abi string) (*removalPlan, error) {
	if running != "" && kernelpkg.MatchesRunning(abi, running) {
		return nil, fmt.Errorf("refusing to remove kernel %s: it is the running kernel (%s)", abi, running)
	}
	if abi == kernelpkg.Newest(abiList(pk)) {
		return nil, fmt.Errorf("refusing to remove kernel %s: it is the newest installed kernel (the newest and the running kernel are always kept)", abi)
	}
	p := &removalPlan{ABI: abi}
	for _, k := range pk {
		if k.ABI == abi {
			p.Packages = append(p.Packages, k.Spec)
			p.Bytes += k.SizeByte
		}
	}
	if len(p.Packages) == 0 {
		return nil, fmt.Errorf("no installed packages found for kernel %s", abi)
	}
	sort.Strings(p.Packages)
	return p, nil
}

// removeCmd returns the package manager command that removes plan.
func removeCmd(mgr string, plan *removalPlan) (env []string, name string, args []string) {
	switch mgr {
	case "apt":
		return []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a", "APT_LISTCHANGES_FRONTEND=none"},
			"apt-get", append([]string{"-q", "-y", "purge"}, plan.Packages...)
	default:
		return nil, mgr, append([]string{"remove", "-y"}, plan.Packages...)
	}
}

// removeKernel implements `priv remove-kernel [--dry-run] ABI`.
func removeKernel(ctx context.Context, w io.Writer, d kernelDeps, dry bool, abi string) error {
	plan, err := planKernelRemoval(ctx, d, abi)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "kernel %s: %d package(s), about %s\n", plan.ABI, len(plan.Packages), humanBytes(plan.Bytes))
	for _, p := range plan.Packages {
		fmt.Fprintf(w, "  %s\n", p)
	}
	fmt.Fprintf(w, "kernel_abi=%s\nkernel_packages=%s\nkernel_bytes=%d\n", plan.ABI, strings.Join(plan.Packages, ","), plan.Bytes)
	if dry {
		fmt.Fprintln(w, "DRY RUN: nothing was removed")
		return nil
	}
	env, name, args := removeCmd(d.mgr, plan)
	return d.exec(ctx, w, env, name, args...)
}

// removeOldKernels removes every kernel except the running and the newest
// (used by the disk cleanup task). It returns the bytes the packages occupy.
func removeOldKernels(ctx context.Context, w io.Writer, d kernelDeps, dry bool) (int64, error) {
	pk, err := installedKernelPkgs(ctx, d)
	if err != nil {
		return 0, err
	}
	old := kernelpkg.Removable(abiList(pk), d.running)
	if len(old) == 0 {
		fmt.Fprintln(w, "no old kernels to remove (the running and newest kernels are kept)")
		return 0, nil
	}
	var total int64
	var failed []string
	for _, abi := range old {
		plan, err := planFrom(pk, d.running, abi)
		if err != nil {
			return total, err
		}
		fmt.Fprintf(w, "old kernel %s: %d package(s), about %s\n", abi, len(plan.Packages), humanBytes(plan.Bytes))
		if dry {
			total += plan.Bytes
			continue
		}
		env, name, args := removeCmd(d.mgr, plan)
		if err := d.exec(ctx, w, env, name, args...); err != nil {
			failed = append(failed, abi)
			continue
		}
		total += plan.Bytes
	}
	if len(failed) > 0 {
		return total, errors.New("could not remove kernel(s): " + strings.Join(failed, ", "))
	}
	return total, nil
}

// parseRemoveKernelArgs parses `[--dry-run] ABI`.
func parseRemoveKernelArgs(args []string) (dry bool, abi string, err error) {
	for _, a := range args {
		switch {
		case a == "--dry-run":
			dry = true
		case abi == "" && !strings.HasPrefix(a, "-"):
			abi = a
		default:
			return false, "", fmt.Errorf("unexpected argument %q", a)
		}
	}
	if !kernelpkg.ValidABI(abi) {
		return false, "", fmt.Errorf("invalid kernel ABI %q", abi)
	}
	return dry, abi, nil
}

// kernelDryRunResult extracts the machine-readable lines of a removal dry run.
func kernelDryRunResult(out string) []byte {
	res := map[string]any{}
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		switch k {
		case "kernel_abi":
			res["abi"] = v
		case "kernel_packages":
			res["packages"] = strings.Split(v, ",")
		case "kernel_bytes":
			n, _ := strconv.ParseInt(v, 10, 64)
			res["bytes"] = n
		}
	}
	if _, ok := res["packages"]; !ok {
		return nil
	}
	return jsonMarshal(res)
}
