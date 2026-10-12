package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	pb "sentrel/gen/sentrelv1"
	"sentrel/internal/jobpolicy"
	"sentrel/internal/kernelpkg"
)

func slogDiscard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// jobWriter streams job output to the server in chunks.
type jobWriter struct {
	a      *Agent
	id     string
	mu     sync.Mutex
	buf    []byte
	all    []byte
	total  int
	timer  *time.Timer
	closed bool
}

const maxJobBytes = 1 << 20

func newJobWriter(a *Agent, id string) *jobWriter { return &jobWriter{a: a, id: id} }

func (w *jobWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return len(p), nil
	}
	if w.total < maxJobBytes {
		room := maxJobBytes - w.total
		chunk := p
		if len(chunk) > room {
			chunk = append(append([]byte{}, chunk[:room]...), []byte("\n[output truncated]\n")...)
		}
		w.buf = append(w.buf, chunk...)
		w.all = append(w.all, chunk...)
		w.total += len(chunk)
	}
	if len(w.buf) >= 16*1024 {
		w.flushLocked()
	} else if w.timer == nil {
		w.timer = time.AfterFunc(500*time.Millisecond, func() {
			w.mu.Lock()
			w.flushLocked()
			w.mu.Unlock()
		})
	}
	return len(p), nil
}

func (w *jobWriter) flushLocked() {
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if len(w.buf) == 0 {
		return
	}
	data := w.buf
	w.buf = nil
	w.a.send(&pb.AgentMessage{Msg: &pb.AgentMessage_JobOutput{JobOutput: &pb.JobOutput{JobId: w.id, Stream: "stdout", Data: data}}})
}

func (w *jobWriter) Close() {
	w.mu.Lock()
	w.flushLocked()
	w.closed = true
	w.mu.Unlock()
}

func (w *jobWriter) Printf(format string, args ...any) { fmt.Fprintf(w, format+"\n", args...) }

// verifyJob checks the signature and freshness of a signed job.
func (a *Agent) verifyJob(sj *pb.SignedJob) (*pb.JobSpec, error) {
	if !ed25519.Verify(a.jobKey, sj.Spec, sj.Signature) {
		return nil, errors.New("invalid job signature")
	}
	var spec pb.JobSpec
	if err := proto.Unmarshal(sj.Spec, &spec); err != nil {
		return nil, err
	}
	if spec.AgentId != a.cfg.AgentID {
		return nil, errors.New("job is for a different agent")
	}
	if err := a.checkWritePolicy(&spec, sj); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if spec.ExpiresUnix < now {
		return nil, errors.New("job has expired")
	}
	if spec.IssuedUnix > now+300 {
		return nil, errors.New("job issued in the future (check the clock)")
	}
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	if _, seen := a.seenJobs[spec.Id]; seen {
		return nil, errors.New("job already processed")
	}
	a.seenJobs[spec.Id] = time.Unix(spec.ExpiresUnix, 0).Add(seenSlack)
	a.saveSeenLocked() // persist before running so a restart cannot replay it
	return &spec, nil
}

// checkWritePolicy enforces the local write policy. It depends only on the
// agent's own configuration and the job bytes, never on server-pushed options.
func (a *Agent) checkWritePolicy(spec *pb.JobSpec, sj *pb.SignedJob) error {
	if jobpolicy.IsSafe(spec.Type) {
		return nil
	}
	switch a.writePolicy {
	case jobpolicy.PolicyNone:
		return errors.New(jobpolicy.ReadOnlyMessage)
	case jobpolicy.PolicyOperator:
		if len(sj.OperatorSignature) == 0 {
			return errors.New("rejected: write jobs on this agent need an operator signature")
		}
		if _, ok := jobpolicy.VerifyOperator(a.opKeys, sj.OperatorKeyId, sj.Spec, sj.OperatorSignature); !ok {
			return errors.New("rejected: invalid operator signature")
		}
	}
	return nil
}

func (a *Agent) result(id, status string, code int, errMsg string, started time.Time, resultJSON []byte) {
	a.send(&pb.AgentMessage{Msg: &pb.AgentMessage_JobResult{JobResult: &pb.JobResult{
		JobId: id, Status: status, ExitCode: int32(code), Error: errMsg, StartedMs: started.UnixMilli(), FinishedMs: time.Now().UnixMilli(), ResultJson: resultJSON,
	}}})
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

func (a *Agent) runJob(ctx context.Context, sj *pb.SignedJob) {
	spec, err := a.verifyJob(sj)
	if err != nil {
		var probe pb.JobSpec
		_ = proto.Unmarshal(sj.Spec, &probe)
		a.log.Warn("rejected job", "id", probe.Id, "type", probe.Type, "err", err)
		if probe.Id != "" && err.Error() != "job already processed" {
			a.result(probe.Id, "rejected", -1, err.Error(), time.Now(), nil)
		}
		return
	}
	a.jobSem <- struct{}{}
	defer func() { <-a.jobSem }()
	started := time.Now()
	a.log.Info("running job", "id", spec.Id, "type", spec.Type, "dry_run", spec.DryRun)
	timeout := time.Duration(spec.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	jctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	w := newJobWriter(a, spec.Id)
	defer w.Close()
	var resJSON []byte
	switch spec.Type {
	case "collect_now":
		kick(a.collectNow)
	case "refresh_inventory":
		inv := a.sendInventory(jctx, true)
		if inv != nil {
			w.Printf("inventory sent: %d installed, %d upgradable", len(inv.Installed), len(inv.Upgradable))
			if inv.Error != "" {
				w.Printf("warning: %s", inv.Error)
			}
		}
	case "upgrade":
		err = a.jobUpgrade(jctx, spec, w)
		if spec.DryRun && err == nil {
			resJSON = dryRunSummary(w)
		}
	case "remove_kernel":
		err = a.jobRemoveKernel(jctx, timeout, spec, w)
		if spec.DryRun && err == nil {
			resJSON = kernelDryRunResult(w.String())
		}
	case "block_ip":
		err = a.privOut(jctx, timeout, w, "block-ip", spec.Params["ip"], spec.Params["minutes"])
		kick(a.blocksNow)
	case "unblock_ip":
		err = a.privOut(jctx, timeout, w, "unblock-ip", spec.Params["ip"])
		kick(a.blocksNow)
	case "disk_usage":
		resJSON, err = a.jobDiskUsage(jctx, timeout, spec.Params, w)
	case "disk_cleanup":
		err = a.jobDiskCleanup(jctx, timeout, spec.DryRun, spec.Params, w)
	case "script":
		err = runScript(jctx, spec.Params["interpreter"], spec.Params["script"], w)
	case "self_update":
		err = a.selfUpdate(jctx, spec, w)
		if err == nil {
			w.Printf("update installed; restarting")
			w.Close()
			a.result(spec.Id, "succeeded", 0, "", started, nil)
			time.Sleep(2 * time.Second) // let the result flush
			a.collector.SaveHistory()
			a.saveSeenJobs()
			// systemd (Restart=always) and the Windows service manager (failure
			// action: restart, which needs a non-zero exit) start the new binary.
			if runtime.GOOS == "windows" {
				os.Exit(1)
			}
			os.Exit(0)
		}
	default:
		err = fmt.Errorf("unsupported job type %q", spec.Type)
	}
	w.Close()
	status := "succeeded"
	msg := ""
	if err != nil {
		status = "failed"
		msg = err.Error()
		if jctx.Err() == context.DeadlineExceeded {
			status = "timeout"
		}
		a.log.Warn("job failed", "id", spec.Id, "type", spec.Type, "err", err)
	}
	a.result(spec.Id, status, exitCode(err), msg, started, resJSON)
}

func (a *Agent) jobUpgrade(ctx context.Context, spec *pb.JobSpec, w *jobWriter) error {
	o := UpgradeOptions{DryRun: spec.DryRun, SecurityOnly: spec.Params["security_only"] == "true"}
	if pk := strings.TrimSpace(spec.Params["packages"]); pk != "" {
		for _, p := range strings.FieldsFunc(pk, func(r rune) bool { return r == ',' || r == ' ' }) {
			if !pkgNameRe.MatchString(p) {
				return fmt.Errorf("invalid package name %q", p)
			}
			o.Packages = append(o.Packages, p)
		}
	}
	// apt has no native security-only upgrade: pick security packages from
	// the current inventory.
	if o.SecurityOnly && len(o.Packages) == 0 && packageManager() == "apt" {
		inv := CollectInventory(ctx, false, nil)
		for _, p := range inv.Upgradable {
			if p.Security {
				o.Packages = append(o.Packages, p.Name)
			}
		}
		if len(o.Packages) == 0 {
			w.Printf("no security updates pending")
			return nil
		}
		w.Printf("security packages: %s", strings.Join(o.Packages, ", "))
	}
	if o.DryRun {
		w.Printf("DRY RUN: no changes will be made")
	}
	err := a.privOut(ctx, time.Duration(spec.TimeoutSeconds)*time.Second, w, "pkg-upgrade", o.args()...)
	if !o.DryRun {
		a.collector.InvalidateReboot()
		kick(a.invNow)
		kick(a.collectNow)
	}
	return err
}

var aptInstRe = regexp.MustCompile(`(?m)^Inst (\S+) \[([^\]]+)\] \((\S+)`)

// dryRunSummary extracts the would-upgrade list from apt simulation output
// so the UI can show a table before the real upgrade is confirmed.
func dryRunSummary(w *jobWriter) []byte {
	w.mu.Lock()
	all := string(w.all)
	w.mu.Unlock()
	type change struct {
		Name string `json:"name"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	var out []change
	for _, m := range aptInstRe.FindAllStringSubmatch(all, -1) {
		out = append(out, change{Name: m[1], From: m[2], To: m[3]})
	}
	if out == nil {
		return nil
	}
	return jsonMarshal(map[string]any{"changes": out})
}

func runScript(ctx context.Context, interpreter, script string, w io.Writer) error {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "windows" && (interpreter == "" || interpreter == "powershell"):
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "-")
	case runtime.GOOS == "windows" && interpreter == "cmd":
		f, err := os.CreateTemp("", "sentrel-*.cmd")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		f.WriteString(script)
		f.Close()
		cmd = exec.CommandContext(ctx, "cmd.exe", "/C", f.Name())
		script = ""
	case interpreter == "bash":
		cmd = exec.CommandContext(ctx, "bash", "-s")
	case interpreter == "python3":
		cmd = exec.CommandContext(ctx, "python3", "-")
	default:
		cmd = exec.CommandContext(ctx, "/bin/sh", "-s")
	}
	if script != "" {
		cmd.Stdin = strings.NewReader(script)
	}
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.WaitDelay = 5 * time.Second
	return cmd.Run()
}

// selfUpdate downloads, verifies and installs a new agent binary.
func (a *Agent) selfUpdate(ctx context.Context, spec *pb.JobSpec, w *jobWriter) error {
	a.mu.Lock()
	if a.updating {
		a.mu.Unlock()
		return errors.New("an update is already in progress")
	}
	a.updating = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.updating = false
		a.mu.Unlock()
	}()
	p := spec.Params
	path, sha, ver, sig := p["path"], p["sha256"], p["version"], p["signature"]
	if !strings.HasPrefix(path, "/download/agent/") || sha == "" || ver == "" || sig == "" {
		return errors.New("incomplete update job")
	}
	if _, err := base64.StdEncoding.DecodeString(sig); err != nil {
		return errors.New("bad signature encoding")
	}
	url := strings.TrimRight(a.cfg.ServerURL, "/") + path
	w.Printf("downloading %s (version %s)", url, ver)
	cl, err := a.cfg.httpClient(10 * time.Minute)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	name := "update.bin"
	if runtime.GOOS == "windows" {
		name = "update.exe"
	}
	stage, err := stagingDir(a.cfg.StateDir)
	if err != nil {
		return err
	}
	dst := filepath.Join(stage, name)
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, 512<<20))
	f.Close()
	if err != nil {
		return err
	}
	if want, _ := strconv.ParseInt(p["size"], 10, 64); want > 0 && n != want {
		return fmt.Errorf("download size %d, expected %d", n, want)
	}
	if err := verifyUpdateFile(dst, sha, ver, sig, a.jobKey, runtime.GOOS, runtime.GOARCH); err != nil {
		os.Remove(dst)
		return err
	}
	w.Printf("signature and checksum verified")
	// The new binary must be able to authenticate before we swap.
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(tctx, dst, "self-test", "--config", a.cfg.Path()).CombinedOutput()
	if err != nil {
		os.Remove(dst)
		return fmt.Errorf("new version failed its self-test: %v: %s", err, strings.TrimSpace(string(out)))
	}
	w.Printf("self-test passed: %s", strings.TrimSpace(string(out)))
	if err := a.privOut(ctx, 2*time.Minute, w, "install-update", dst, sha, ver, sig); err != nil {
		return err
	}
	_ = os.Remove(dst)
	if stage != a.cfg.StateDir {
		_ = os.RemoveAll(stage)
	}
	return nil
}

func jsonMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// String returns everything written so far.
func (w *jobWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.all)
}

// jobRemoveKernel removes one old kernel ABI through the privileged helper.
// The helper refuses the running and the newest installed kernel.
func (a *Agent) jobRemoveKernel(ctx context.Context, timeout time.Duration, spec *pb.JobSpec, w *jobWriter) error {
	if runtime.GOOS != "linux" {
		return errors.New("kernel removal is not applicable on this operating system")
	}
	abi := strings.TrimSpace(spec.Params["abi"])
	if !kernelpkg.ValidABI(abi) {
		return fmt.Errorf("invalid kernel ABI %q", abi)
	}
	args := []string{abi}
	if spec.DryRun {
		w.Printf("DRY RUN: no changes will be made")
		args = append([]string{"--dry-run"}, args...)
	}
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	err := a.privOut(ctx, timeout, w, "remove-kernel", args...)
	if !spec.DryRun {
		a.collector.InvalidateReboot()
		kick(a.invNow) // sends the inventory so the server rescans for CVEs
		kick(a.collectNow)
	}
	return err
}
