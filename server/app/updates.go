package app

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"sentrel/internal/release"
	"sentrel/internal/version"
	"sentrel/server/selfupdate"
)

// Update checking and one-click updates.
//
// The only automatic outbound request Sentrel makes is the version check: a
// GET to the GitHub releases API carrying a User-Agent of "Sentrel/<version>"
// and an ETag. Nothing about the installation is sent. It can be turned off in
// Settings -> Updates, or with SENTREL_DISABLE_UPDATE_CHECK=1.

const (
	githubAPI      = "https://api.github.com"
	githubDownload = "https://github.com"
	checkInterval  = 6 * time.Hour
	checkTimeout   = 10 * time.Second
	maxReleaseJSON = 4 << 20
	maxNotesBytes  = 64 << 10

	envDisableCheck   = "SENTREL_DISABLE_UPDATE_CHECK"
	envDisableInstall = "SENTREL_DISABLE_SELF_UPDATE"

	keyUpdateAuditMark = "update_audit_mark"

	serverBinPath    = "/usr/local/bin/sentrel-server"
	updaterPathUnit  = "/etc/systemd/system/sentrel-updater.path"
	updaterSvcUnit   = "/etc/systemd/system/sentrel-updater.service"
	phaseDownloading = "downloading"
	phaseVerifying   = "verifying"
	phaseWaiting     = "waiting_for_updater"
	phaseFailed      = "failed"
	phaseDone        = "done"
	phaseRolledBack  = "rolled_back"
	phaseIdle        = "idle"
)

// Install methods.
const (
	MethodDocker  = "docker"
	MethodPackage = "package"
	MethodSystemd = "systemd"
	MethodManual  = "manual"
)

// UpdateSettings is the "updates" settings section.
type UpdateSettings struct {
	CheckEnabled   bool `json:"check_enabled"`
	InstallEnabled bool `json:"install_enabled"`
}

// SettingsStore accessor.
func (s *SettingsStore) Updates() UpdateSettings { return get[UpdateSettings](s, keyUpdates) }

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// releaseInfo is the part of a GitHub release that is kept.
type releaseInfo struct {
	Tag         string         `json:"tag"`
	Version     string         `json:"version"`
	PublishedAt int64          `json:"published_at"`
	HTMLURL     string         `json:"html_url"`
	Body        string         `json:"body"`
	Assets      []releaseAsset `json:"assets"`
}

func (r *releaseInfo) hasAsset(name string) bool {
	for _, a := range r.Assets {
		if a.Name == name {
			return true
		}
	}
	return false
}

// installEnv abstracts the host so install-method detection can be tested.
type installEnv struct {
	GOOS     string
	Getenv   func(string) string
	Exists   func(path string) bool
	ReadFile func(path string) ([]byte, error)
	Exe      func() (string, error)
	// Owns reports whether the package manager named by tool ("dpkg-query"
	// or "rpm") owns the file.
	Owns func(tool, path string) bool
}

func realInstallEnv() installEnv {
	return installEnv{
		GOOS:   runtime.GOOS,
		Getenv: os.Getenv,
		Exists: func(p string) bool { _, err := os.Stat(p); return err == nil },
		ReadFile: func(p string) ([]byte, error) {
			return os.ReadFile(p)
		},
		Exe: func() (string, error) {
			p, err := os.Executable()
			if err != nil {
				return "", err
			}
			if r, err := filepath.EvalSymlinks(p); err == nil {
				p = r
			}
			return p, nil
		},
		Owns: func(tool, path string) bool {
			bin, err := exec.LookPath(tool)
			if err != nil {
				return false
			}
			args := []string{"-S", path}
			if tool == "rpm" {
				args = []string{"-qf", path}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, bin, args...).Run() == nil
		},
	}
}

// detectInstallMethod classifies how this server was installed. Only
// MethodSystemd supports one-click updates.
func detectInstallMethod(e installEnv, dataDir string) string {
	if e.Getenv("SENTREL_INSTALL_METHOD") == "docker" || e.Exists("/.dockerenv") {
		return MethodDocker
	}
	if e.GOOS != "linux" {
		return MethodManual
	}
	exe, err := e.Exe()
	if err != nil {
		return MethodManual
	}
	if e.Owns("dpkg-query", exe) || e.Owns("rpm", exe) {
		return MethodPackage
	}
	if e.Getenv("INVOCATION_ID") != "" && exe == serverBinPath && e.Exists(updaterPathUnit) {
		if svc, err := e.ReadFile(updaterSvcUnit); err == nil && strings.Contains(string(svc), "--data-dir "+dataDir) {
			return MethodSystemd
		}
	}
	return MethodManual
}

func envTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// installProgress is the in-memory part of an install (download and verify).
type installProgress struct {
	Phase     string
	Version   string
	Error     string
	StartedAt int64
}

type updateService struct {
	a *App

	// Overridable in tests only.
	apiBase  string
	dlBase   string
	client   *http.Client
	env      installEnv
	key      ed25519.PublicKey
	goarch   string
	current  string
	getenv   func(string) string
	dataDir  string
	interval time.Duration

	methodOnce sync.Once
	method     string

	mu        sync.Mutex
	latest    *releaseInfo
	etag      string
	lastCheck int64
	lastError string
	inst      installProgress
	running   bool
}

func newUpdateService(a *App) *updateService {
	return &updateService{
		a: a, apiBase: githubAPI, dlBase: githubDownload,
		client: &http.Client{Timeout: checkTimeout}, env: realInstallEnv(),
		key: release.TrustRoot(), goarch: runtime.GOARCH, current: version.Version,
		getenv: os.Getenv, dataDir: a.cfg.DataDir, interval: checkInterval,
	}
}

func (u *updateService) installMethod() string {
	u.methodOnce.Do(func() { u.method = detectInstallMethod(u.env, u.dataDir) })
	return u.method
}

// checkState reports whether checking is on and whether the administrator of
// the host forced it off.
func (u *updateService) checkState() (enabled, forcedOff bool) {
	if envTrue(u.getenv(envDisableCheck)) {
		return false, true
	}
	return u.a.settings.Updates().CheckEnabled, false
}

func (u *updateService) installState() (enabled, forcedOff bool) {
	if envTrue(u.getenv(envDisableInstall)) {
		return false, true
	}
	return u.a.settings.Updates().InstallEnabled, false
}

// Check fetches the latest release. A 304 keeps the stored release.
func (u *updateService) Check(ctx context.Context) error {
	if on, _ := u.checkState(); !on {
		return errUpdatesDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	rel, etag, notModified, err := u.fetchLatest(ctx)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.lastCheck = time.Now().Unix()
	if err != nil {
		u.lastError = err.Error()
		return err
	}
	u.lastError = ""
	if !notModified {
		u.latest, u.etag = rel, etag
	}
	return nil
}

var errUpdatesDisabled = errors.New("update checks are disabled")

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (u *updateService) fetchLatest(ctx context.Context) (rel *releaseInfo, etag string, notModified bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.apiBase+"/repos/"+release.Repo+"/releases/latest", nil)
	if err != nil {
		return nil, "", false, err
	}
	// Nothing else is sent: no install id, no host data.
	req.Header.Set("User-Agent", "Sentrel/"+u.current)
	req.Header.Set("Accept", "application/vnd.github+json")
	u.mu.Lock()
	if u.etag != "" && u.latest != nil {
		req.Header.Set("If-None-Match", u.etag)
	}
	u.mu.Unlock()
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return nil, "", true, nil
	case resp.StatusCode == http.StatusNotFound:
		return nil, "", false, errors.New("no releases found")
	case resp.StatusCode != http.StatusOK:
		return nil, "", false, fmt.Errorf("GitHub answered HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseJSON))
	if err != nil {
		return nil, "", false, err
	}
	var g ghRelease
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, "", false, fmt.Errorf("unexpected response: %w", err)
	}
	if g.Draft || g.Prerelease {
		return nil, "", false, errors.New("latest release is a draft or prerelease; ignoring")
	}
	ver := strings.TrimPrefix(g.TagName, "v")
	if _, ok := release.ParseVersion(ver); !ok || release.IsPrerelease(ver) {
		return nil, "", false, fmt.Errorf("release tag %q is not a release version", g.TagName)
	}
	body := g.Body
	if len(body) > maxNotesBytes {
		body = body[:maxNotesBytes]
	}
	rel = &releaseInfo{Tag: g.TagName, Version: ver, PublishedAt: g.PublishedAt.Unix(), HTMLURL: g.HTMLURL, Body: body}
	for _, as := range g.Assets {
		rel.Assets = append(rel.Assets, releaseAsset{Name: as.Name, URL: as.URL, Size: as.Size})
	}
	return rel, resp.Header.Get("ETag"), false, nil
}

// loop checks once shortly after start-up and then every six hours.
func (u *updateService) loop(ctx context.Context) {
	// The root updater writes result.json after the restart, so a freshly
	// started server usually has to wait for it: poll for it while running.
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	next := time.Now().Add(45 * time.Second)
	for {
		u.reportLastResult(ctx)
		if !time.Now().Before(next) {
			next = time.Now().Add(u.interval)
			if on, _ := u.checkState(); on {
				if err := u.Check(ctx); err != nil {
					u.a.log.Debug("update check failed", "err", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// updateAvailable reports the stored release when it is newer than this build.
func (u *updateService) updateAvailable() (*releaseInfo, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.latest != nil && release.Newer(u.current, u.latest.Version) {
		cp := *u.latest
		return &cp, true
	}
	return nil, false
}

// ---- install -------------------------------------------------------------

// installBlocker returns why a one-click install cannot start, as a 409.
func (u *updateService) installBlocker(rel *releaseInfo) error {
	if _, forced := u.installState(); forced {
		return conflict("one-click updates are disabled by the administrator of this host")
	}
	if on, _ := u.installState(); !on {
		return conflict("one-click updates are turned off in Settings -> Updates")
	}
	if m := u.installMethod(); m != MethodSystemd {
		return conflict("one-click updates need the systemd install of install-server.sh (this server is installed as: " + m + ")")
	}
	if rel == nil {
		return conflict("no newer release is known; check for updates first")
	}
	return nil
}

// Install validates a request and starts the download in the background.
func (u *updateService) Install(ver, actor string) error {
	rel, ok := u.updateAvailable()
	if !ok {
		return conflict("no newer release is known; check for updates first")
	}
	if err := u.installBlocker(rel); err != nil {
		return err
	}
	if strings.TrimPrefix(ver, "v") != rel.Version {
		return badRequest("the latest release is %s, not %s; refresh and try again", rel.Version, ver)
	}
	asset := release.ServerAsset(u.goarch)
	for _, n := range []string{asset, selfupdate.SumsName, selfupdate.SigName} {
		if !rel.hasAsset(n) {
			return conflict("release " + rel.Version + " does not include " + n + "; update manually")
		}
	}
	u.mu.Lock()
	if u.running {
		u.mu.Unlock()
		return conflict("an update is already in progress")
	}
	if st := selfupdate.ReadState(u.dataDir); st.Request != nil || st.Status != nil {
		u.mu.Unlock()
		return conflict("the updater is still working on a previous request")
	}
	u.running = true
	u.inst = installProgress{Phase: phaseDownloading, Version: rel.Version, StartedAt: time.Now().Unix()}
	u.mu.Unlock()

	go func() {
		err := u.stage(context.WithoutCancel(u.a.ctx), rel, actor)
		u.mu.Lock()
		defer u.mu.Unlock()
		u.running = false
		if err != nil {
			u.inst.Phase, u.inst.Error = phaseFailed, err.Error()
			u.a.log.Error("update staging failed", "version", rel.Version, "err", err)
			u.a.audit(context.Background(), auditEntry{Actor: actor, Action: "server.update_failed", TargetType: "server", Details: map[string]any{"version": rel.Version, "error": err.Error()}})
			return
		}
		u.inst.Phase = phaseWaiting
	}()
	return nil
}

func (u *updateService) setPhase(p string) {
	u.mu.Lock()
	u.inst.Phase = p
	u.mu.Unlock()
}

// stage downloads and verifies the release, then hands it to the updater by
// writing request.json.
func (u *updateService) stage(ctx context.Context, rel *releaseInfo, actor string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := selfupdate.Reset(u.dataDir); err != nil {
		return fmt.Errorf("clearing previous update files: %w", err)
	}
	staged := selfupdate.StagedPath(u.dataDir)
	if err := os.MkdirAll(staged, 0o750); err != nil {
		return fmt.Errorf("creating the staging directory: %w", err)
	}
	asset := release.ServerAsset(u.goarch)
	base := u.dlBase + "/" + release.Repo + "/releases/download/" + rel.Tag + "/"
	if _, err := u.download(ctx, base+selfupdate.SumsName, filepath.Join(staged, selfupdate.SumsName), selfupdate.MaxSmallBytes); err != nil {
		return err
	}
	if _, err := u.download(ctx, base+selfupdate.SigName, filepath.Join(staged, selfupdate.SigName), selfupdate.MaxSmallBytes); err != nil {
		return err
	}
	sum, err := u.download(ctx, base+asset, filepath.Join(staged, asset), selfupdate.MaxBinaryBytes)
	if err != nil {
		return err
	}
	u.setPhase(phaseVerifying)
	sums, _ := os.ReadFile(filepath.Join(staged, selfupdate.SumsName))
	sig, _ := os.ReadFile(filepath.Join(staged, selfupdate.SigName))
	if err := release.VerifyBundle(u.key, sums, sig, asset, sum); err != nil {
		_ = selfupdate.Reset(u.dataDir)
		return fmt.Errorf("verification failed, nothing was installed: %w", err)
	}
	req := selfupdate.Request{Version: rel.Version, Asset: asset, Files: []string{asset, selfupdate.SumsName, selfupdate.SigName},
		RequestedBy: actor, RequestedAt: time.Now().Unix()}
	if err := selfupdate.WriteRequest(u.dataDir, req); err != nil {
		return fmt.Errorf("writing the update request: %w", err)
	}
	return nil
}

// download fetches url into dst (never following an existing file) and returns
// the SHA-256 of what was written.
func (u *updateService) download(ctx context.Context, url, dst string, limit int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Sentrel/"+u.current)
	cl := &http.Client{Transport: u.client.Transport, Timeout: 0}
	resp, err := cl.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", filepath.Base(dst), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", filepath.Base(dst), resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("download %s: %w", filepath.Base(dst), err)
	}
	if n > limit {
		return "", fmt.Errorf("download %s: larger than the %d byte limit", filepath.Base(dst), limit)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// InstallStatus is what GET /server/updates/status returns.
type InstallStatus struct {
	Phase       string `json:"phase"`
	Version     string `json:"version,omitempty"`
	Message     string `json:"message,omitempty"`
	From        string `json:"from,omitempty"`
	StartedAt   int64  `json:"started_at,omitempty"`
	FinishedAt  int64  `json:"finished_at,omitempty"`
	RequestedAt int64  `json:"requested_at,omitempty"`
	RolledBack  bool   `json:"rolled_back,omitempty"`
}

// Status combines the in-memory download phases with the files the root
// updater writes.
func (u *updateService) Status() InstallStatus {
	u.mu.Lock()
	in := u.inst
	u.mu.Unlock()
	switch in.Phase {
	case phaseDownloading, phaseVerifying:
		return InstallStatus{Phase: in.Phase, Version: in.Version, StartedAt: in.StartedAt}
	case phaseFailed:
		return InstallStatus{Phase: phaseFailed, Version: in.Version, Message: in.Error, StartedAt: in.StartedAt}
	}
	st := selfupdate.ReadState(u.dataDir)
	switch {
	case st.Request != nil:
		return InstallStatus{Phase: phaseWaiting, Version: st.Request.Version, RequestedAt: st.Request.RequestedAt, StartedAt: in.StartedAt}
	case st.Status != nil:
		return InstallStatus{Phase: st.Status.Phase, Version: st.Status.Version, StartedAt: in.StartedAt}
	case st.Result != nil:
		out := InstallStatus{Phase: phaseDone, Version: st.Result.To, From: st.Result.From, Message: st.Result.Message,
			StartedAt: st.Result.StartedAt, FinishedAt: st.Result.FinishedAt, RolledBack: st.Result.RolledBack}
		switch st.Result.Outcome {
		case selfupdate.OutcomeRolledBack:
			out.Phase = phaseRolledBack
		case selfupdate.OutcomeFailed:
			out.Phase = phaseFailed
		}
		return out
	}
	if in.Phase == phaseWaiting {
		// The request is gone but no status or result exists yet.
		return InstallStatus{Phase: phaseWaiting, Version: in.Version, StartedAt: in.StartedAt}
	}
	return InstallStatus{Phase: phaseIdle}
}

// reportLastResult writes an audit entry, once, for an update the root
// updater finished since the last start.
func (u *updateService) reportLastResult(ctx context.Context) {
	st := selfupdate.ReadState(u.dataDir)
	if st.Result == nil {
		return
	}
	var mark struct {
		FinishedAt int64 `json:"finished_at"`
	}
	_ = u.a.settings.Load(0, keyUpdateAuditMark, &mark)
	if mark.FinishedAt == st.Result.FinishedAt {
		return
	}
	action := "server.update_applied"
	switch st.Result.Outcome {
	case selfupdate.OutcomeRolledBack:
		action = "server.update_rolled_back"
	case selfupdate.OutcomeFailed:
		action = "server.update_refused"
	}
	u.a.audit(ctx, auditEntry{Actor: "updater", Action: action, TargetType: "server", TargetID: st.Result.To,
		Details: map[string]any{"from": st.Result.From, "to": st.Result.To, "message": st.Result.Message}})
	mark.FinishedAt = st.Result.FinishedAt
	_ = u.a.settings.Save(ctx, 0, keyUpdateAuditMark, mark)
}

// ---- API -----------------------------------------------------------------

func isUpdateAdmin(c *Ctx) bool { return c.P.Has(PermSettings) && c.P.IsProvider() }

func (a *App) apiGetUpdates(c *Ctx) (any, error) {
	u := a.updates
	admin := isUpdateAdmin(c)
	checkOn, checkForced := u.checkState()
	installOn, installForced := u.installState()
	rel, avail := u.updateAvailable()
	u.mu.Lock()
	latest, lastCheck, lastErr := u.latest, u.lastCheck, u.lastError
	u.mu.Unlock()
	out := map[string]any{
		"current":          u.current,
		"update_available": avail,
		"check_enabled":    checkOn,
	}
	if latest != nil {
		out["latest"] = latest.Version
	}
	if !admin {
		return out, nil
	}
	out["install_enabled"] = installOn
	out["check_forced_off"] = checkForced
	out["install_forced_off"] = installForced
	out["last_check"] = lastCheck
	out["last_error"] = lastErr
	out["method"] = u.installMethod()
	out["repo"] = release.Repo
	out["hostname"], _ = os.Hostname()
	if latest != nil {
		out["published_at"] = latest.PublishedAt
		out["html_url"] = latest.HTMLURL
		out["notes"] = latest.Body
		out["tag"] = latest.Tag
		out["assets"] = latest.Assets
	}
	out["can_install"] = avail && u.installBlocker(rel) == nil
	if avail {
		if err := u.installBlocker(rel); err != nil {
			out["install_blocked"] = err.Error()
		}
	}
	return out, nil
}

func (a *App) apiCheckUpdates(c *Ctx) (any, error) {
	if !isUpdateAdmin(c) {
		return nil, forbidden("only a server administrator can check for updates")
	}
	if err := a.updates.Check(c.ctx()); err != nil {
		if errors.Is(err, errUpdatesDisabled) {
			return nil, conflict("update checks are disabled")
		}
		// The failure is also recorded as last_error; return the state.
	}
	return a.apiGetUpdates(c)
}

type installUpdateReq struct {
	Version string `json:"version"`
	Confirm bool   `json:"confirm"`
}

func (a *App) apiInstallUpdate(c *Ctx) (any, error) {
	if !isUpdateAdmin(c) {
		return nil, forbidden("only a server administrator can install updates")
	}
	var req installUpdateReq
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	if !req.Confirm {
		return nil, badRequest("confirmation required: send confirm=true")
	}
	if err := a.updates.Install(req.Version, c.P.Name); err != nil {
		return nil, err
	}
	c.audit("server.update_requested", "server", req.Version, map[string]any{"from": a.updates.current, "to": strings.TrimPrefix(req.Version, "v")})
	return a.updates.Status(), nil
}

func (a *App) apiUpdateStatus(c *Ctx) (any, error) {
	if !isUpdateAdmin(c) {
		return nil, forbidden("only a server administrator can see update progress")
	}
	return a.updates.Status(), nil
}
