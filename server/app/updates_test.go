package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sentrel/internal/release"
	"sentrel/server/selfupdate"
)

func fakeEnv(over func(*installEnv)) installEnv {
	e := installEnv{
		GOOS:     "linux",
		Getenv:   func(string) string { return "" },
		Exists:   func(string) bool { return false },
		ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
		Exe:      func() (string, error) { return "/opt/sentrel/sentrel-server", nil },
		Owns:     func(string, string) bool { return false },
	}
	if over != nil {
		over(&e)
	}
	return e
}

func TestDetectInstallMethod(t *testing.T) {
	const data = "/var/lib/sentrel"
	systemd := func(e *installEnv) {
		e.Getenv = func(k string) string {
			if k == "INVOCATION_ID" {
				return "abc"
			}
			return ""
		}
		e.Exe = func() (string, error) { return serverBinPath, nil }
		e.Exists = func(p string) bool { return p == updaterPathUnit || p == updaterSvcUnit }
		e.ReadFile = func(p string) ([]byte, error) {
			return []byte("ExecStart=" + serverBinPath + " self-update apply --data-dir " + data + "\n"), nil
		}
	}
	cases := []struct {
		name string
		over func(*installEnv)
		want string
	}{
		{"plain run", nil, MethodManual},
		{"dockerenv", func(e *installEnv) { e.Exists = func(p string) bool { return p == "/.dockerenv" } }, MethodDocker},
		{"env docker", func(e *installEnv) {
			e.Getenv = func(k string) string {
				if k == "SENTREL_INSTALL_METHOD" {
					return "docker"
				}
				return ""
			}
		}, MethodDocker},
		{"docker beats systemd", func(e *installEnv) {
			systemd(e)
			e.Exists = func(string) bool { return true }
		}, MethodDocker},
		{"systemd", systemd, MethodSystemd},
		{"systemd, binary elsewhere", func(e *installEnv) {
			systemd(e)
			e.Exe = func() (string, error) { return "/opt/sentrel-server", nil }
		}, MethodManual},
		{"systemd without updater units", func(e *installEnv) {
			systemd(e)
			e.Exists = func(string) bool { return false }
		}, MethodManual},
		{"units for another data dir", func(e *installEnv) {
			systemd(e)
			e.ReadFile = func(string) ([]byte, error) { return []byte("--data-dir /srv/other"), nil }
		}, MethodManual},
		{"not started by systemd", func(e *installEnv) {
			systemd(e)
			e.Getenv = func(string) string { return "" }
		}, MethodManual},
		{"dpkg package", func(e *installEnv) {
			systemd(e)
			e.Owns = func(tool, _ string) bool { return tool == "dpkg-query" }
		}, MethodPackage},
		{"rpm package", func(e *installEnv) { e.Owns = func(tool, _ string) bool { return tool == "rpm" } }, MethodPackage},
		{"windows", func(e *installEnv) { e.GOOS = "windows" }, MethodManual},
		{"no executable path", func(e *installEnv) { e.Exe = func() (string, error) { return "", os.ErrNotExist } }, MethodManual},
	}
	for _, c := range cases {
		if got := detectInstallMethod(fakeEnv(c.over), data); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func ghJSON(tag string, draft, pre bool, assets ...string) string {
	var as []string
	for _, n := range assets {
		as = append(as, fmt.Sprintf(`{"name":%q,"browser_download_url":"https://github.com/x/y/releases/download/%s/%s","size":10}`, n, tag, n))
	}
	return fmt.Sprintf(`{"tag_name":%q,"draft":%v,"prerelease":%v,"published_at":"2026-10-01T10:00:00Z","html_url":"https://github.com/PacketGhost99/Sentrel/releases/tag/%s","body":"## Notes\n- <b>x</b>","assets":[%s]}`,
		tag, draft, pre, tag, strings.Join(as, ","))
}

func TestUpdateCheck(t *testing.T) {
	e := newTestEnv(t)
	u := e.a.updates
	u.current = "0.9.1-dev"
	var hits atomic.Int32
	var gotHeaders http.Header
	var gotPath string
	body := ghJSON("v0.9.2", false, false, "SHA256SUMS")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotHeaders, gotPath = r.Header.Clone(), r.URL.Path
		if r.Header.Get("If-None-Match") == `"etag1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"etag1"`)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	u.apiBase = srv.URL

	if err := u.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/repos/PacketGhost99/Sentrel/releases/latest" {
		t.Errorf("path %s", gotPath)
	}
	if gotHeaders.Get("User-Agent") != "Sentrel/0.9.1-dev" {
		t.Errorf("User-Agent %q", gotHeaders.Get("User-Agent"))
	}
	for h := range gotHeaders {
		switch http.CanonicalHeaderKey(h) {
		case "User-Agent", "Accept", "Accept-Encoding", "If-None-Match":
		default:
			t.Errorf("unexpected request header %s: %v", h, gotHeaders[h])
		}
	}
	rel, ok := u.updateAvailable()
	if !ok || rel.Version != "0.9.2" || rel.Tag != "v0.9.2" || !strings.Contains(rel.Body, "<b>x</b>") || len(rel.Assets) != 1 || rel.PublishedAt == 0 {
		t.Fatalf("release: %+v ok=%v", rel, ok)
	}
	// Second check sends the ETag and keeps the stored release on a 304.
	if err := u.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if gotHeaders.Get("If-None-Match") != `"etag1"` {
		t.Errorf("If-None-Match %q", gotHeaders.Get("If-None-Match"))
	}
	if _, ok := u.updateAvailable(); !ok {
		t.Error("release lost after 304")
	}
	// Up to date once the running version catches up; prerelease builds are offered the release.
	u.current = "0.9.2"
	if _, ok := u.updateAvailable(); ok {
		t.Error("equal version reported as an update")
	}
	u.current = "0.9.3-dev"
	if _, ok := u.updateAvailable(); ok {
		t.Error("older release reported as an update")
	}
	u.current = "0.9.2-dev"
	if _, ok := u.updateAvailable(); !ok {
		t.Error("0.9.2-dev should be offered 0.9.2")
	}
}

func TestUpdateCheckIgnoresDraftsPrereleasesAndErrors(t *testing.T) {
	e := newTestEnv(t)
	u := e.a.updates
	u.current = "0.9.0"
	var reply string
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(reply))
	}))
	defer srv.Close()
	u.apiBase = srv.URL
	for name, r := range map[string]string{
		"draft":      ghJSON("v0.9.5", true, false),
		"prerelease": ghJSON("v0.9.5", false, true),
		"rc tag":     ghJSON("v0.9.5-rc.1", false, false),
		"bad tag":    ghJSON("nightly", false, false),
		"garbage":    "<html>",
	} {
		reply = r
		if err := u.Check(t.Context()); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if _, ok := u.updateAvailable(); ok {
			t.Errorf("%s: offered", name)
		}
	}
	u.mu.Lock()
	if u.lastError == "" || u.lastCheck == 0 {
		t.Error("last error and time not recorded")
	}
	u.mu.Unlock()
	status = 500
	if err := u.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("500: %v", err)
	}
	u.apiBase = "http://127.0.0.1:1"
	if err := u.Check(t.Context()); err == nil {
		t.Error("unreachable server should error")
	}
}

func TestUpdateCheckDisabled(t *testing.T) {
	e := newTestEnv(t)
	u := e.a.updates
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	u.apiBase = srv.URL

	// Environment variable forces it off even though the setting is on.
	u.getenv = func(k string) string {
		if k == "SENTREL_DISABLE_UPDATE_CHECK" {
			return "1"
		}
		return ""
	}
	if on, forced := u.checkState(); on || !forced {
		t.Errorf("env: on=%v forced=%v", on, forced)
	}
	if err := u.Check(t.Context()); err != errUpdatesDisabled {
		t.Errorf("err %v", err)
	}
	// The setting turns it off.
	u.getenv = func(string) string { return "" }
	admin := e.setupAdmin()
	okDo(t, admin, "PUT", "/settings/updates", map[string]any{"check_enabled": false, "install_enabled": true})
	if on, forced := u.checkState(); on || forced {
		t.Errorf("setting: on=%v forced=%v", on, forced)
	}
	if code, _, _ := admin.do("POST", "/server/updates/check", nil); code != 409 {
		t.Errorf("check while disabled: %d", code)
	}
	if hits.Load() != 0 {
		t.Errorf("%d requests were made while disabled", hits.Load())
	}
}

func TestUpdateSettingsDefaults(t *testing.T) {
	e := newTestEnv(t)
	if s := e.a.settings.Updates(); !s.CheckEnabled || !s.InstallEnabled {
		t.Errorf("defaults %+v", s)
	}
}

func TestUpdateAPIPermissions(t *testing.T) {
	e := newTestEnv(t)
	u := e.a.updates
	u.current = "0.9.1-dev"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"e"`)
		w.Write([]byte(ghJSON("v0.9.2", false, false, "SHA256SUMS", "SHA256SUMS.sig", "sentrel-server-linux-amd64")))
	}))
	defer srv.Close()
	u.apiBase = srv.URL

	admin := e.setupAdmin()
	okDo(t, admin, "POST", "/users", map[string]any{"username": "viewer", "password": testPassword, "role": "viewer"})
	okDo(t, admin, "POST", "/users", map[string]any{"username": "oper", "password": testPassword, "role": "operator"})
	tid := int64(okDo(t, admin, "POST", "/tenants", map[string]any{"name": "Acme"})["id"].(float64))
	okDo(t, admin, "POST", "/users", map[string]any{"tenant_id": tid, "username": "tadmin", "password": testPassword, "role": "tenant_admin"})
	viewer, oper, tadmin := e.login("viewer", testPassword), e.login("oper", testPassword), e.login("tadmin", testPassword)

	// Admin checks, then everyone logged in can see that an update exists.
	m := okDo(t, admin, "POST", "/server/updates/check", nil)
	if m["latest"] != "0.9.2" || m["update_available"] != true || m["method"] == nil || m["notes"] == nil || m["last_check"] == nil {
		t.Fatalf("admin view: %v", m)
	}
	for name, cl := range map[string]*client{"viewer": viewer, "operator": oper, "tenant admin": tadmin} {
		m := okDo(t, cl, "GET", "/server/updates", nil)
		if m["current"] != "0.9.1-dev" || m["update_available"] != true || m["latest"] != "0.9.2" {
			t.Errorf("%s sees %v", name, m)
		}
		for _, k := range []string{"notes", "method", "assets", "html_url", "hostname", "last_error", "repo", "can_install"} {
			if _, leaked := m[k]; leaked {
				t.Errorf("%s sees admin-only field %q", name, k)
			}
		}
		for _, call := range []struct{ method, path string }{
			{"POST", "/server/updates/check"}, {"POST", "/server/updates/install"}, {"GET", "/server/updates/status"},
		} {
			if code, _, _ := cl.do(call.method, call.path, map[string]any{"version": "0.9.2", "confirm": true}); code != 403 {
				t.Errorf("%s %s %s: %d, want 403", name, call.method, call.path, code)
			}
		}
	}
	if code, _, _ := e.client().do("GET", "/server/updates", nil); code != 401 {
		t.Errorf("anonymous: %d", code)
	}

	// Admin install: not systemd -> 409; without confirm -> 400.
	if code, _, _ := admin.do("POST", "/server/updates/install", map[string]any{"version": "0.9.2"}); code != 400 {
		t.Errorf("no confirm: %d", code)
	}
	code, m2, raw := admin.do("POST", "/server/updates/install", map[string]any{"version": "0.9.2", "confirm": true})
	if code != 409 {
		t.Errorf("manual install method: %d %s", code, raw)
	}
	_ = m2
	st := okDo(t, admin, "GET", "/server/updates/status", nil)
	if st["phase"] != "idle" {
		t.Errorf("status %v", st)
	}
	// Forced off by the host administrator.
	u.getenv = func(k string) string {
		if k == "SENTREL_DISABLE_SELF_UPDATE" {
			return "1"
		}
		return ""
	}
	m = okDo(t, admin, "GET", "/server/updates", nil)
	if m["install_forced_off"] != true || m["can_install"] != false {
		t.Errorf("forced-off view: %v", m)
	}
	if code, _, _ := admin.do("POST", "/server/updates/install", map[string]any{"version": "0.9.2", "confirm": true}); code != 409 {
		t.Errorf("forced-off install: %d", code)
	}
}

// installHarness serves a release the way GitHub's download URLs do.
type installHarness struct {
	e    *testEnv
	u    *updateService
	priv ed25519.PrivateKey
	// served files by asset name
	files map[string][]byte
	rel   *releaseInfo
}

func newInstallHarness(t *testing.T) *installHarness {
	t.Helper()
	e := newTestEnv(t)
	u := e.a.updates
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := &installHarness{e: e, u: u, priv: priv, files: map[string][]byte{}}
	u.key, u.goarch, u.current = pub, "amd64", "0.9.1-dev"
	u.methodOnce.Do(func() {})
	u.method = MethodSystemd
	asset := release.ServerAsset("amd64")
	bin := []byte("pretend binary 0.9.2")
	sums := []byte(release.SHA256Hex(bin) + "  " + asset + "\n")
	h.files[asset], h.files["SHA256SUMS"] = bin, sums
	h.files["SHA256SUMS.sig"] = []byte(release.Sign(priv, sums) + "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/" + release.Repo + "/releases/download/v0.9.2/"
		name := strings.TrimPrefix(r.URL.Path, prefix)
		b, ok := h.files[name]
		if !strings.HasPrefix(r.URL.Path, prefix) || !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	u.dlBase = srv.URL
	h.rel = &releaseInfo{Tag: "v0.9.2", Version: "0.9.2", PublishedAt: time.Now().Unix()}
	for n := range h.files {
		h.rel.Assets = append(h.rel.Assets, releaseAsset{Name: n})
	}
	u.latest = h.rel
	return h
}

func (h *installHarness) wait(t *testing.T, want string) InstallStatus {
	t.Helper()
	var st InstallStatus
	for i := 0; i < 200; i++ {
		st = h.u.Status()
		if st.Phase == want {
			return st
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("status never reached %q, last %+v", want, st)
	return st
}

func TestInstallStagesAndRequests(t *testing.T) {
	h := newInstallHarness(t)
	dir := h.u.dataDir
	// Leftovers of an earlier run must not survive.
	os.MkdirAll(selfupdate.StagedPath(dir), 0o750)
	os.WriteFile(filepath.Join(selfupdate.StagedPath(dir), "stale"), []byte("x"), 0o640)
	os.WriteFile(filepath.Join(selfupdate.Dir(dir), selfupdate.ResultFile), []byte(`{"outcome":"success"}`), 0o640)

	if err := h.u.Install("0.9.2", "alice"); err != nil {
		t.Fatal(err)
	}
	st := h.wait(t, phaseWaiting)
	if st.Version != "0.9.2" || st.RequestedAt == 0 {
		t.Errorf("status %+v", st)
	}
	state := selfupdate.ReadState(dir)
	if state.Request == nil || state.Request.Version != "0.9.2" || state.Request.RequestedBy != "alice" || state.Result != nil {
		t.Fatalf("state %+v", state)
	}
	for name, want := range h.files {
		got, err := os.ReadFile(filepath.Join(selfupdate.StagedPath(dir), name))
		if err != nil || string(got) != string(want) {
			t.Errorf("staged %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(selfupdate.StagedPath(dir), "stale")); err == nil {
		t.Error("stale staged file kept")
	}
	// While the request is pending, another install is refused.
	if err := h.u.Install("0.9.2", "bob"); err == nil || !strings.Contains(err.Error(), "previous request") {
		t.Errorf("second install: %v", err)
	}
	// The updater's progress files are reflected.
	os.Remove(filepath.Join(selfupdate.Dir(dir), selfupdate.RequestFile))
	os.WriteFile(filepath.Join(selfupdate.Dir(dir), selfupdate.StatusFile), []byte(`{"phase":"restarting","version":"0.9.2"}`), 0o640)
	if st := h.u.Status(); st.Phase != "restarting" {
		t.Errorf("status %+v", st)
	}
	os.Remove(filepath.Join(selfupdate.Dir(dir), selfupdate.StatusFile))
	os.WriteFile(filepath.Join(selfupdate.Dir(dir), selfupdate.ResultFile), []byte(`{"outcome":"rolled_back","from":"0.9.1-dev","to":"0.9.2","message":"unhealthy","rolled_back":true}`), 0o640)
	if st := h.u.Status(); st.Phase != phaseRolledBack || !st.RolledBack || st.Message != "unhealthy" {
		t.Errorf("status %+v", st)
	}
}

func TestInstallRejectsBadReleases(t *testing.T) {
	asset := release.ServerAsset("amd64")
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	cases := []struct {
		name    string
		mutate  func(h *installHarness)
		wantErr string // staging failure message ("" = rejected up front)
	}{
		{"bad signature", func(h *installHarness) {
			h.files["SHA256SUMS.sig"] = []byte(release.Sign(otherPriv, h.files["SHA256SUMS"]))
		}, "verification failed"},
		{"hash mismatch", func(h *installHarness) { h.files[asset] = []byte("evil") }, "does not match"},
		{"empty signature", func(h *installHarness) { h.files["SHA256SUMS.sig"] = []byte("\n") }, "verification failed"},
		{"download 404", func(h *installHarness) { delete(h.files, asset) }, "HTTP 404"},
		{"wrong arch listed", func(h *installHarness) {
			h.files["SHA256SUMS"] = []byte(strings.Repeat("a", 64) + "  sentrel-server-linux-arm64\n")
			h.files["SHA256SUMS.sig"] = []byte(release.Sign(h.priv, h.files["SHA256SUMS"]))
		}, "not listed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newInstallHarness(t)
			c.mutate(h)
			if err := h.u.Install("0.9.2", "alice"); err != nil {
				t.Fatal(err)
			}
			st := h.wait(t, phaseFailed)
			if !strings.Contains(st.Message, c.wantErr) {
				t.Errorf("message %q lacks %q", st.Message, c.wantErr)
			}
			if state := selfupdate.ReadState(h.u.dataDir); state.Request != nil {
				t.Error("request.json written for an unverified release")
			}
			// A failed attempt can be retried.
			h.u.mu.Lock()
			running := h.u.running
			h.u.mu.Unlock()
			if running {
				t.Error("still marked running")
			}
		})
	}
}

func TestInstallUpFrontRefusals(t *testing.T) {
	h := newInstallHarness(t)
	if err := h.u.Install("0.9.9", "a"); err == nil || !strings.Contains(err.Error(), "latest release is 0.9.2") {
		t.Errorf("wrong version: %v", err)
	}
	h.u.current = "0.9.2"
	if err := h.u.Install("0.9.2", "a"); err == nil {
		t.Error("equal version accepted")
	}
	h.u.current = "0.9.3"
	if err := h.u.Install("0.9.2", "a"); err == nil {
		t.Error("downgrade accepted")
	}
	h.u.current = "0.9.1-dev"
	h.u.latest = &releaseInfo{Tag: "v0.9.2", Version: "0.9.2", Assets: []releaseAsset{{Name: "SHA256SUMS"}}}
	if err := h.u.Install("0.9.2", "a"); err == nil || !strings.Contains(err.Error(), "does not include") {
		t.Errorf("missing assets: %v", err)
	}
	h.u.latest = h.rel
	h.u.method = MethodDocker
	if err := h.u.Install("0.9.2", "a"); err == nil || !strings.Contains(err.Error(), "docker") {
		t.Errorf("docker: %v", err)
	}
}

func TestProductionTrustRootIsCompiledIn(t *testing.T) {
	e := newTestEnv(t)
	if string(e.a.updates.key) != string(release.TrustRoot()) {
		t.Fatal("the update service must use the compiled-in trust root by default")
	}
}
