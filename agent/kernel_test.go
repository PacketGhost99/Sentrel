package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

const dpkgOut = "ii \tlinux-image-7.0.0-28-generic\t7.0.0-28.28\t14000\n" +
	"ii \tlinux-modules-7.0.0-28-generic\t7.0.0-28.28\t90000\n" +
	"ii \tlinux-headers-7.0.0-28\t7.0.0-28.28\t12000\n" +
	"ii \tlinux-headers-7.0.0-28-generic\t7.0.0-28.28\t3000\n" +
	"ii \tlinux-image-7.0.0-38-generic\t7.0.0-38.38\t14000\n" +
	"ii \tlinux-modules-7.0.0-38-generic\t7.0.0-38.38\t90000\n" +
	"rc \tlinux-image-6.8.0-1-generic\t6.8.0-1\t1\n" +
	"ii \tlinux-generic\t7.0.0.38\t10\n" +
	"ii \tlinux-image-generic\t7.0.0.38\t10\n" +
	"ii \tbash\t5.2\t1000\n"

type recorder struct {
	calls [][]string
	err   error
}

func newDeps(rec *recorder, running string) kernelDeps {
	return kernelDeps{mgr: "apt", running: running,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte(dpkgOut), nil
		},
		exec: func(ctx context.Context, w io.Writer, env []string, name string, args ...string) error {
			rec.calls = append(rec.calls, append([]string{name}, args...))
			return rec.err
		}}
}

func TestRemoveKernelRefusals(t *testing.T) {
	rec := &recorder{}
	d := newDeps(rec, "7.0.0-38-generic")
	var out bytes.Buffer
	for _, c := range []struct{ abi, want string }{
		{"7.0.0-38", "running kernel"},
		{"6.8.0-1", "no installed packages"},
		{"7.0.0-28; rm -rf /", "invalid kernel ABI"},
		{"", "invalid kernel ABI"},
		{"$(id)", "invalid kernel ABI"},
	} {
		err := removeKernel(context.Background(), &out, d, false, c.abi)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v want %q", c.abi, err, c.want)
		}
	}
	if len(rec.calls) != 0 {
		t.Fatalf("a refused removal ran commands: %v", rec.calls)
	}
}

func TestRemoveKernelRefusesNewest(t *testing.T) {
	rec := &recorder{}
	// Running the older kernel: the newest (7.0.0-38) must still be kept.
	d := newDeps(rec, "7.0.0-28-generic")
	err := removeKernel(context.Background(), io.Discard, d, false, "7.0.0-38")
	if err == nil || !strings.Contains(err.Error(), "newest") {
		t.Fatalf("got %v", err)
	}
	if len(rec.calls) != 0 {
		t.Fatal("ran a command")
	}
}

func TestRemoveKernelDryRunListsOnlyThatABI(t *testing.T) {
	rec := &recorder{}
	d := newDeps(rec, "7.0.0-38-generic")
	var out bytes.Buffer
	if err := removeKernel(context.Background(), &out, d, true, "7.0.0-28"); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Fatal("dry run executed a command")
	}
	s := out.String()
	for _, want := range []string{"linux-image-7.0.0-28-generic", "linux-modules-7.0.0-28-generic", "linux-headers-7.0.0-28", "linux-headers-7.0.0-28-generic"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	for _, bad := range []string{"7.0.0-38", "6.8.0-1", "linux-generic", "bash"} {
		if strings.Contains(s, bad) {
			t.Errorf("unexpected %s in %s", bad, s)
		}
	}
	res := string(kernelDryRunResult(s))
	if !strings.Contains(res, `"abi":"7.0.0-28"`) || !strings.Contains(res, `"packages":[`) || strings.Contains(res, "7.0.0-38") {
		t.Errorf("result json: %s", res)
	}
}

func TestRemoveKernelPurgesExactNames(t *testing.T) {
	rec := &recorder{}
	d := newDeps(rec, "7.0.0-38-generic")
	if err := removeKernel(context.Background(), io.Discard, d, false, "7.0.0-28"); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("calls: %v", rec.calls)
	}
	got := strings.Join(rec.calls[0], " ")
	want := "apt-get -q -y purge linux-headers-7.0.0-28 linux-headers-7.0.0-28-generic linux-image-7.0.0-28-generic linux-modules-7.0.0-28-generic"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestRemoveKernelPropagatesFailure(t *testing.T) {
	rec := &recorder{err: errors.New("boom")}
	if err := removeKernel(context.Background(), io.Discard, newDeps(rec, "7.0.0-38-generic"), false, "7.0.0-28"); err == nil {
		t.Fatal("expected failure")
	}
}

func TestRemoveKernelRPM(t *testing.T) {
	rec := &recorder{}
	rpmOut := "kernel\t0\t5.14.0-427.13.1.el9_4\tx86_64\t100\n" +
		"kernel-core\t0\t5.14.0-427.13.1.el9_4\tx86_64\t200\n" +
		"kernel-modules-core\t0\t5.14.0-427.13.1.el9_4\tx86_64\t300\n" +
		"kernel\t0\t5.14.0-503.11.1.el9_5\tx86_64\t100\n" +
		"kernel-core\t0\t5.14.0-503.11.1.el9_5\tx86_64\t200\n" +
		"kernel-headers\t0\t5.14.0-503.11.1.el9_5\tx86_64\t50\n" +
		"bash\t0\t5.1-1\tx86_64\t5\n"
	d := kernelDeps{mgr: "dnf", running: "5.14.0-503.11.1.el9_5.x86_64",
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) { return []byte(rpmOut), nil },
		exec: func(ctx context.Context, w io.Writer, env []string, name string, args ...string) error {
			rec.calls = append(rec.calls, append([]string{name}, args...))
			return nil
		}}
	if err := removeKernel(context.Background(), io.Discard, d, true, "5.14.0-427.13.1.el9_4"); err != nil || len(rec.calls) != 0 {
		t.Fatal(err, rec.calls)
	}
	if err := removeKernel(context.Background(), io.Discard, d, false, "5.14.0-427.13.1.el9_4"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rec.calls[0], " ")
	want := "dnf remove -y kernel-5.14.0-427.13.1.el9_4.x86_64 kernel-core-5.14.0-427.13.1.el9_4.x86_64 kernel-modules-core-5.14.0-427.13.1.el9_4.x86_64"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if err := removeKernel(context.Background(), io.Discard, d, false, "5.14.0-503.11.1.el9_5"); err == nil {
		t.Fatal("removed the running kernel")
	}
}

func TestRemoveOldKernelsKeepsRunningAndNewest(t *testing.T) {
	rec := &recorder{}
	d := newDeps(rec, "7.0.0-38-generic")
	var out bytes.Buffer
	n, err := removeOldKernels(context.Background(), &out, d, true)
	if err != nil || len(rec.calls) != 0 {
		t.Fatal(err, rec.calls)
	}
	if n != (14000+90000+12000+3000)*1024 {
		t.Errorf("dry run size %d", n)
	}
	if _, err := removeOldKernels(context.Background(), io.Discard, d, false); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 || strings.Contains(strings.Join(rec.calls[0], " "), "7.0.0-38") {
		t.Fatalf("calls %v", rec.calls)
	}
	// Only the running kernel installed: nothing to do.
	one := kernelDeps{mgr: "apt", running: "7.0.0-38-generic", exec: d.exec,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte("ii \tlinux-image-7.0.0-38-generic\t1\t1\n"), nil
		}}
	rec.calls = nil
	if n, err := removeOldKernels(context.Background(), io.Discard, one, false); err != nil || n != 0 || len(rec.calls) != 0 {
		t.Fatal(n, err, rec.calls)
	}
}

func TestParseRemoveKernelArgs(t *testing.T) {
	if dry, abi, err := parseRemoveKernelArgs([]string{"--dry-run", "7.0.0-28"}); err != nil || !dry || abi != "7.0.0-28" {
		t.Fatal(dry, abi, err)
	}
	for _, a := range [][]string{{}, {"--bogus", "7.0.0-28"}, {"7.0.0-28", "7.0.0-29"}, {"7.0.0"}, {"--dry-run"}} {
		if _, _, err := parseRemoveKernelArgs(a); err == nil {
			t.Errorf("%v accepted", a)
		}
	}
}
