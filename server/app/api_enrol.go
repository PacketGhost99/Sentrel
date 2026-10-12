package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"heartbeat/deploy"
	"heartbeat/server/agentdist"
	"heartbeat/server/db"
	"heartbeat/server/pushinstall"
)

type tokenReq struct {
	TenantID   int64    `json:"tenant_id"`
	GroupID    *int64   `json:"group_id"`
	Tags       []string `json:"tags"`
	HostName   string   `json:"host_name"`
	TTLMinutes int      `json:"ttl_minutes"`
}

type tokenResp struct {
	ID            int64             `json:"id"`
	Token         string            `json:"token"`
	ExpiresAt     int64             `json:"expires_at"`
	ServerURL     string            `json:"server_url"`
	CAFingerprint string            `json:"ca_fingerprint,omitempty"`
	Commands      map[string]string `json:"commands"`
}

func formatToken(raw string) string {
	return raw[0:4] + "-" + raw[4:8] + "-" + raw[8:12]
}

// createToken makes a one-time enrolment token.
func (a *App) createToken(ctx context.Context, p *Principal, tenant int64, group *int64, tags []string, hostName string, ttl time.Duration) (*EnrollmentToken, string, error) {
	tok := formatToken(randomCode(12))
	now := db.Now()
	res, err := a.db.ExecContext(ctx, `INSERT INTO enrollment_tokens (tenant_id, token_hash, group_id, tags, host_name, created_by, expires_at, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		tenant, hashToken(normaliseToken(tok)), nullID(group), StrList(cleanTags(tags)), strings.TrimSpace(hostName), p.UserID, now+int64(ttl.Seconds()), now)
	if err != nil {
		return nil, "", err
	}
	id, _ := res.LastInsertId()
	var et EnrollmentToken
	err = a.db.GetContext(ctx, &et, `SELECT * FROM enrollment_tokens WHERE id=?`, id)
	return &et, tok, err
}

// installCommands builds copy-paste install commands for a token.
func (a *App) installCommands(serverURL, token string) (map[string]string, string) {
	selfSigned := strings.HasPrefix(serverURL, "https://") && a.srv.Mode() == "selfsigned"
	fp := ""
	if selfSigned {
		fp = a.ca.Fingerprint()
	}
	k, shExtra, psExtra, psPre := "", "", "", ""
	if selfSigned {
		k = "k"
		shExtra = fmt.Sprintf(" --ca-fingerprint %s --insecure-download", fp)
		psExtra = fmt.Sprintf(" -CaFingerprint %s -InsecureDownload", fp)
		psPre = "[Net.ServicePointManager]::ServerCertificateValidationCallback={$true}; "
	}
	cmds := map[string]string{
		"linux":   fmt.Sprintf("curl -fsSL%s %s/install/agent.sh | sudo sh -s -- --server %s --token %s%s", k, serverURL, serverURL, token, shExtra),
		"windows": fmt.Sprintf("%s[Net.ServicePointManager]::SecurityProtocol='Tls12'; & ([scriptblock]::Create((irm %s/install/agent.ps1))) -Server %s -Token %s%s", psPre, serverURL, serverURL, token, psExtra),
		"manual":  fmt.Sprintf("heartbeat-agent register %s %s", serverURL, token),
	}
	if fp != "" {
		cmds["manual"] += " --ca-fingerprint " + fp
	}
	return cmds, fp
}

func (a *App) apiCreateToken(c *Ctx) (any, error) {
	var req tokenReq
	_ = c.Decode(&req)
	tenant, err := c.tenantFor(req.TenantID)
	if err != nil {
		return nil, err
	}
	ttl := time.Duration(req.TTLMinutes) * time.Minute
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if ttl > 7*24*time.Hour {
		return nil, badRequest("tokens can be valid for at most 7 days")
	}
	et, tok, err := a.createToken(c.ctx(), c.P, tenant, req.GroupID, req.Tags, req.HostName, ttl)
	if err != nil {
		return nil, err
	}
	url := a.publicURL(c.R)
	cmds, fp := a.installCommands(url, tok)
	c.auditT(tenant, "enrollment_token.created", "token", itoa(et.ID), map[string]any{"ttl_minutes": int(ttl.Minutes()), "host_name": req.HostName})
	return tokenResp{ID: et.ID, Token: tok, ExpiresAt: et.ExpiresAt, ServerURL: url, CAFingerprint: fp, Commands: cmds}, nil
}

func (a *App) apiListTokens(c *Ctx) (any, error) {
	tw, args := c.tenantWhere("tenant_id")
	out := []EnrollmentToken{}
	err := a.db.SelectContext(c.ctx(), &out, `SELECT * FROM enrollment_tokens WHERE used_at=0 AND expires_at>? AND `+tw+` ORDER BY created_at DESC`, append([]any{db.Now()}, args...)...)
	return out, err
}

func (a *App) apiDeleteToken(c *Ctx) (any, error) {
	tw, args := c.tenantWhere("tenant_id")
	res, err := a.db.ExecContext(c.ctx(), `DELETE FROM enrollment_tokens WHERE id=? AND `+tw, append([]any{c.ID()}, args...)...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errNotFound
	}
	c.audit("enrollment_token.deleted", "token", itoa(c.ID()), nil)
	return nil, nil
}

// --- push install ---

type pushCommon struct {
	TenantID int64    `json:"tenant_id"`
	GroupID  *int64   `json:"group_id"`
	Tags     []string `json:"tags"`
	Name     string   `json:"name"`
}

type pushSSHReq struct {
	pushCommon
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	PrivateKey    string `json:"private_key"`
	KeyPassphrase string `json:"key_passphrase"`
	SudoPassword  string `json:"sudo_password"`
	HostKey       string `json:"host_key_fingerprint"`
	HostKeyAlias  string `json:"host_key"` // accepted as an alias of host_key_fingerprint
}

type pushWinRMReq struct {
	pushCommon
	Host     string `json:"host"`
	Port     int    `json:"port"`
	HTTPS    bool   `json:"https"`
	Insecure bool   `json:"insecure"`
	Username string `json:"username"`
	Password string `json:"password"`
	NTLM     bool   `json:"ntlm"`
	// AllowInsecure must be true to use plain HTTP or skip certificate verification.
	AllowInsecure bool `json:"allow_insecure"`
}

type pushService struct {
	app *App
	mu  sync.Mutex
}

func newPushService(a *App) *pushService { return &pushService{app: a} }

func (s *pushService) start(c *Ctx, method, target, user string, pc pushCommon, run func(ctx context.Context, token, serverURL string, log pushinstall.Logger) error) (*PushInstall, error) {
	a := s.app
	tenant, err := c.tenantFor(pc.TenantID)
	if err != nil {
		return nil, err
	}
	if a.settings.General().ServerURL == "" {
		return nil, badRequest("set the server URL in Settings → General first; remote hosts need it to reach this server")
	}
	et, tok, err := a.createToken(c.ctx(), c.P, tenant, pc.GroupID, pc.Tags, pc.Name, 30*time.Minute)
	if err != nil {
		return nil, err
	}
	now := db.Now()
	res, err := a.db.ExecContext(c.ctx(), `INSERT INTO push_installs (tenant_id, method, target, username, status, created_by, created_at) VALUES (?,?,?,?,?,?,?)`,
		tenant, method, target, user, "running", c.P.Name, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	c.auditT(tenant, "push_install.started", "push_install", itoa(id), map[string]any{"method": method, "target": target, "username": user})
	serverURL := strings.TrimRight(a.settings.General().ServerURL, "/")
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 15*time.Minute)
		defer cancel()
		var mu sync.Mutex
		var lines []string
		logf := func(l string) {
			mu.Lock()
			lines = append(lines, time.Now().UTC().Format("15:04:05")+" "+l)
			text := strings.Join(lines, "\n")
			mu.Unlock()
			_, _ = a.db.Exec(`UPDATE push_installs SET log=? WHERE id=?`, truncateStr(text, 256*1024), id)
			a.events.publish(Event{Type: "push.updated", TenantID: tenant, ID: itoa(id)})
		}
		err := run(ctx, tok, serverURL, logf)
		status := "succeeded"
		var hostID *int64
		if err != nil {
			status = "failed"
			logf("ERROR: " + err.Error())
		} else {
			// Wait for the agent to register with the token.
			logf("waiting for the agent to register")
			deadline := time.Now().Add(2 * time.Minute)
			for time.Now().Before(deadline) {
				var used EnrollmentToken
				if a.db.Get(&used, `SELECT * FROM enrollment_tokens WHERE id=?`, et.ID) == nil && used.UsedByHost != nil {
					hostID = used.UsedByHost
					break
				}
				time.Sleep(2 * time.Second)
			}
			if hostID == nil {
				status = "failed"
				logf("installer finished but the agent did not register; check that the host can reach " + serverURL)
			} else {
				logf(fmt.Sprintf("agent registered as host #%d", *hostID))
			}
		}
		_, _ = a.db.Exec(`UPDATE push_installs SET status=?, host_id=?, finished_at=? WHERE id=?`, status, hostID, db.Now(), id)
		a.audit(context.Background(), auditEntry{TenantID: tenant, Actor: c.P.Name, Action: "push_install." + status, TargetType: "push_install", TargetID: itoa(id)})
		a.events.publish(Event{Type: "push.updated", TenantID: tenant, ID: itoa(id)})
	}()
	var pi PushInstall
	err = a.db.GetContext(c.ctx(), &pi, `SELECT * FROM push_installs WHERE id=?`, id)
	return &pi, err
}

func (a *App) apiPushSSH(c *Ctx) (any, error) {
	var req pushSSHReq
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	if req.Host == "" || req.Username == "" {
		return nil, badRequest("host and username are required")
	}
	if req.HostKey == "" {
		req.HostKey = req.HostKeyAlias
	}
	if strings.TrimSpace(req.HostKey) == "" {
		return nil, badRequest("host_key is required: fetch the host key fingerprint with POST /push/ssh/fingerprint, confirm it, then send it as host_key")
	}
	return a.push.start(c, "ssh", req.Host, req.Username, req.pushCommon, func(ctx context.Context, token, serverURL string, log pushinstall.Logger) error {
		args := []string{"--server", serverURL, "--token", token}
		cmds, fp := a.installCommands(serverURL, token)
		_ = cmds
		if fp != "" {
			args = append(args, "--ca-fingerprint", fp)
		}
		if req.Name != "" {
			args = append(args, "--name", req.Name)
		}
		bin := func(goos, goarch string) (io.ReadCloser, int64, error) {
			b, err := a.agentBinary(goos, goarch)
			if err != nil {
				return nil, 0, err
			}
			rc, err := b.Open()
			return rc, b.Size, err
		}
		_, err := pushinstall.SSHInstall(ctx, pushinstall.SSHRequest{Host: req.Host, Port: req.Port, Username: req.Username, Password: req.Password,
			PrivateKey: req.PrivateKey, KeyPassphrase: req.KeyPassphrase, SudoPassword: req.SudoPassword, HostKey: req.HostKey},
			deploy.AgentShell, bin, args, log)
		return err
	})
}

type pushFingerprintReq struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// apiPushSSHFingerprint returns the SSH host key fingerprint without authenticating.
func (a *App) apiPushSSHFingerprint(c *Ctx) (any, error) {
	var req pushFingerprintReq
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	req.Host = strings.TrimSpace(req.Host)
	if req.Host == "" {
		return nil, badRequest("host is required")
	}
	if req.Port < 0 || req.Port > 65535 {
		return nil, badRequest("invalid port")
	}
	ctx, cancel := context.WithTimeout(c.ctx(), 25*time.Second)
	defer cancel()
	fp, kt, err := pushinstall.SSHFingerprint(ctx, req.Host, req.Port)
	if err != nil {
		return nil, badRequest("%v", err)
	}
	return map[string]any{"host": req.Host, "port": req.Port, "fingerprint": fp, "key_type": kt}, nil
}

func (a *App) apiPushWinRM(c *Ctx) (any, error) {
	var req pushWinRMReq
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	if req.Host == "" || req.Username == "" || req.Password == "" {
		return nil, badRequest("host, username and password are required")
	}
	if (!req.HTTPS || req.Insecure) && !req.AllowInsecure {
		return nil, badRequest("WinRM over plain HTTP or with certificate checks disabled exposes the password to anyone on the network path; use https with a trusted certificate or set allow_insecure to true to accept the risk")
	}
	return a.push.start(c, "winrm", req.Host, req.Username, req.pushCommon, func(ctx context.Context, token, serverURL string, log pushinstall.Logger) error {
		cmds, _ := a.installCommands(serverURL, token)
		script := "$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue'; " + cmds["windows"]
		if req.Name != "" {
			script += " -Name '" + strings.ReplaceAll(req.Name, "'", "''") + "'"
		}
		return pushinstall.WinRMInstall(ctx, pushinstall.WinRMRequest{Host: req.Host, Port: req.Port, HTTPS: req.HTTPS, Insecure: req.Insecure,
			Username: req.Username, Password: req.Password, UseNTLM: req.NTLM}, script, log)
	})
}

func (a *App) apiListPush(c *Ctx) (any, error) {
	tw, args := c.tenantWhere("tenant_id")
	out := []PushInstall{}
	err := a.db.SelectContext(c.ctx(), &out, `SELECT id, tenant_id, method, target, username, status, '' AS log, host_id, created_by, created_at, finished_at
		FROM push_installs WHERE `+tw+` ORDER BY id DESC LIMIT 100`, args...)
	return out, err
}

func (a *App) apiGetPush(c *Ctx) (any, error) {
	var pi PushInstall
	if err := a.db.GetContext(c.ctx(), &pi, `SELECT * FROM push_installs WHERE id=?`, c.ID()); err != nil || !c.P.CanTenant(pi.TenantID) {
		return nil, errNotFound
	}
	return pi, nil
}

func (a *App) apiAgentBinaries(c *Ctx) (any, error) {
	out := agentdist.List(a.cfg.AgentBinDir)
	if out == nil {
		out = []*agentdist.Binary{}
	}
	return out, nil
}

// --- groups ---

type groupReq struct {
	TenantID    int64  `json:"tenant_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
}

func (a *App) apiListGroups(c *Ctx) (any, error) {
	tw, args := c.tenantWhere("g.tenant_id")
	out := []HostGroup{}
	err := a.db.SelectContext(c.ctx(), &out, `SELECT g.*, (SELECT COUNT(*) FROM hosts h WHERE h.group_id=g.id) AS host_count FROM host_groups g WHERE `+tw+` ORDER BY g.name COLLATE NOCASE`, args...)
	return out, err
}

func (a *App) apiCreateGroup(c *Ctx) (any, error) {
	var req groupReq
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	tenant, err := c.tenantFor(req.TenantID)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 64 {
		return nil, badRequest("name must be 1-64 characters")
	}
	res, err := a.db.ExecContext(c.ctx(), `INSERT INTO host_groups (tenant_id, name, description, color, created_at) VALUES (?,?,?,?,?)`,
		tenant, name, truncateStr(req.Description, 500), req.Color, db.Now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	c.auditT(tenant, "group.created", "group", itoa(id), map[string]any{"name": name})
	var g HostGroup
	err = a.db.GetContext(c.ctx(), &g, `SELECT *, 0 AS host_count FROM host_groups WHERE id=?`, id)
	return created{g}, err
}

func (a *App) visibleGroup(c *Ctx, id int64) (*HostGroup, error) {
	var g HostGroup
	if err := a.db.GetContext(c.ctx(), &g, `SELECT *, 0 AS host_count FROM host_groups WHERE id=?`, id); err != nil || !c.P.CanTenant(g.TenantID) {
		return nil, errNotFound
	}
	return &g, nil
}

func (a *App) apiUpdateGroup(c *Ctx) (any, error) {
	g, err := a.visibleGroup(c, c.ID())
	if err != nil {
		return nil, err
	}
	var req groupReq
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, badRequest("name is required")
	}
	_, err = a.db.ExecContext(c.ctx(), `UPDATE host_groups SET name=?, description=?, color=? WHERE id=?`, name, truncateStr(req.Description, 500), req.Color, g.ID)
	c.auditT(g.TenantID, "group.updated", "group", itoa(g.ID), map[string]any{"name": name})
	return nil, err
}

func (a *App) apiDeleteGroup(c *Ctx) (any, error) {
	g, err := a.visibleGroup(c, c.ID())
	if err != nil {
		return nil, err
	}
	ctx := c.ctx()
	_, _ = a.db.ExecContext(ctx, `DELETE FROM threshold_rules WHERE scope='group' AND scope_id=?`, g.ID)
	if _, err := a.db.ExecContext(ctx, `DELETE FROM host_groups WHERE id=?`, g.ID); err != nil {
		return nil, err
	}
	_ = a.alerts.ReloadRules(ctx)
	_ = a.alerts.refreshHosts(ctx)
	c.auditT(g.TenantID, "group.deleted", "group", itoa(g.ID), map[string]any{"name": g.Name})
	return nil, nil
}

func (a *App) apiListTags(c *Ctx) (any, error) {
	tw, args := c.tenantWhere("tenant_id")
	var raw []StrList
	_ = a.db.SelectContext(c.ctx(), &raw, `SELECT tags FROM hosts WHERE `+tw, args...)
	seen := map[string]int{}
	for _, l := range raw {
		for _, t := range l {
			seen[t]++
		}
	}
	type tc struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}
	out := []tc{}
	for t, n := range seen {
		out = append(out, tc{t, n})
	}
	sortBy(out, func(x tc) string { return strings.ToLower(x.Tag) })
	return out, nil
}
