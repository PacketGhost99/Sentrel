package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"

	"heartbeat/server/db"
)

// Settings sections. Each is stored as one JSON row in the settings table.
// Secrets are kept in the same row but stripped by the API on read.

type GeneralSettings struct {
	ServerURL      string `json:"server_url"`       // public URL agents and links use
	SetupCompleted bool   `json:"setup_completed"`
	Timezone       string `json:"timezone"`
}

type TLSSettings struct {
	Mode       string `json:"mode"`        // http, selfsigned, acme
	Listen     string `json:"listen"`      // e.g. 0.0.0.0:8080
	Hostname   string `json:"hostname"`    // for selfsigned SAN / ACME
	ExtraNames []string `json:"extra_names"`
	ACMEEmail  string `json:"acme_email"`
	ACMEDirectory string `json:"acme_directory"` // optional, defaults to Let's Encrypt production
	TrustProxy bool   `json:"trust_proxy"` // honour X-Forwarded-For / X-Forwarded-Proto
}

type SMTPSettings struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	From     string `json:"from"`
	TLSMode  string `json:"tls_mode"` // starttls, tls, none
	SkipVerify bool `json:"skip_verify"`
}

type LDAPSettings struct {
	Enabled       bool              `json:"enabled"`
	URL           string            `json:"url"` // ldap://host:389 or ldaps://host:636
	StartTLS      bool              `json:"start_tls"`
	SkipVerify    bool              `json:"skip_verify"`
	BindDN        string            `json:"bind_dn"`
	BindPassword  string            `json:"bind_password,omitempty"`
	BaseDN        string            `json:"base_dn"`
	UserFilter    string            `json:"user_filter"` // (&(objectClass=person)(uid=%s))
	EmailAttr     string            `json:"email_attr"`
	NameAttr      string            `json:"name_attr"`
	GroupAttr     string            `json:"group_attr"` // memberOf
	RoleMapping   []LDAPRoleMapping `json:"role_mapping"`
	DefaultRole   string            `json:"default_role"` // empty = deny users with no mapping
	DefaultTenant int64             `json:"default_tenant"`
}

type LDAPRoleMapping struct {
	Group    string `json:"group"` // group DN (case-insensitive)
	Role     string `json:"role"`
	TenantID int64  `json:"tenant_id"`
}

type RADIUSSettings struct {
	Enabled       bool   `json:"enabled"`
	Server        string `json:"server"` // host:port
	Secret        string `json:"secret,omitempty"`
	NASIdentifier string `json:"nas_identifier"`
	TimeoutSec    int    `json:"timeout_seconds"`
	DefaultRole   string `json:"default_role"`
	DefaultTenant int64  `json:"default_tenant"`
	// Optional role from the Filter-Id / Class attribute ("heartbeat:admin").
	RoleAttribute string `json:"role_attribute"`
}

type SecuritySettings struct {
	Require2FA        string `json:"require_2fa"` // none, admins, all
	SessionHours      int    `json:"session_hours"`
	PasswordMinLength int    `json:"password_min_length"`
	LockoutAttempts   int    `json:"lockout_attempts"`
	LockoutMinutes    int    `json:"lockout_minutes"`
	MetricsAuth       bool   `json:"metrics_auth"` // /metrics requires an API key
}

type RetentionSettings struct {
	Days                  int   `json:"days"`
	HostStorageWarnMB     int64 `json:"host_storage_warn_mb"`
	DiskWarnPercent       int   `json:"disk_warn_percent"`
	AlertHistoryDays      int   `json:"alert_history_days"`
	AuditDays             int   `json:"audit_days"`
	JobHistoryDays        int   `json:"job_history_days"`
	SecurityEventDays     int   `json:"security_event_days"`
}

type AgentDefaults struct {
	IntervalSeconds        int      `json:"interval_seconds"`
	InventoryIntervalSec   int      `json:"inventory_interval_seconds"`
	TopProcesses           int      `json:"top_processes"`
	CollectServices        bool     `json:"collect_services"`
	CollectPorts           bool     `json:"collect_ports"`
	CollectDocker          bool     `json:"collect_docker"`
	CollectTemperatures    bool     `json:"collect_temperatures"`
	BruteforceEnabled      bool     `json:"bruteforce_enabled"`
	SecurityIntervalSec    int      `json:"security_interval_seconds"`
	Fail2banEnabled        bool     `json:"fail2ban_enabled"`
	RefreshPackageIndex    bool     `json:"refresh_package_index"`
	WatchedServices        []string `json:"watched_services"`
	BruteforceWindowMin    int      `json:"bruteforce_window_minutes"`
	AutoBlockMinutes       int      `json:"auto_block_minutes"`
	AutoBlockThreshold     int      `json:"auto_block_threshold"`
	AutoBlockWhitelist     []string `json:"auto_block_whitelist"`
	AutoUpdate             bool     `json:"auto_update"` // push agent updates automatically
}

type AlertingSettings struct {
	GroupWaitSeconds     int `json:"group_wait_seconds"`
	DefaultRepeatMinutes int `json:"default_repeat_minutes"`
	FlapWindowMinutes    int `json:"flap_window_minutes"`
	FlapThreshold        int `json:"flap_threshold"`
	HostDownSeconds      int `json:"host_down_seconds"`
	ResolveCount         int `json:"resolve_count"`
}

type VulnSettings struct {
	Enabled            bool   `json:"enabled"`
	DBURL              string `json:"db_url"` // empty = Anchore default; set for air-gapped mirrors
	SyncHour           int    `json:"sync_hour"`
	ScanAfterInventory bool   `json:"scan_after_inventory"`
	AlertSeverity      string `json:"alert_severity"` // Critical, High, Medium, none
	OnlyFixable        bool   `json:"only_fixable"`
}

type Branding struct {
	ProductName string `json:"product_name"`
	LogoDataURL string `json:"logo_data_url"`
	FaviconDataURL string `json:"favicon_data_url"`
	AccentColor string `json:"accent_color"`
	LoginText   string `json:"login_text"`
	FooterText  string `json:"footer_text"`
}

const (
	keyGeneral   = "general"
	keyTLS       = "tls"
	keySMTP      = "smtp"
	keyLDAP      = "ldap"
	keyRADIUS    = "radius"
	keySecurity  = "security"
	keyRetention = "retention"
	keyAgent     = "agent_defaults"
	keyAlerting  = "alerting"
	keyVuln      = "vuln"
	keyBranding  = "branding"
)

func defaultFor(key string) any {
	switch key {
	case keyGeneral:
		return &GeneralSettings{Timezone: "UTC"}
	case keyTLS:
		return &TLSSettings{Mode: "http", Listen: "0.0.0.0:8080"}
	case keySMTP:
		return &SMTPSettings{Port: 587, TLSMode: "starttls"}
	case keyLDAP:
		return &LDAPSettings{UserFilter: "(&(objectClass=person)(uid=%s))", EmailAttr: "mail", NameAttr: "cn", GroupAttr: "memberOf"}
	case keyRADIUS:
		return &RADIUSSettings{NASIdentifier: "heartbeat", TimeoutSec: 5, RoleAttribute: "Filter-Id"}
	case keySecurity:
		return &SecuritySettings{Require2FA: "none", SessionHours: 12, PasswordMinLength: 10, LockoutAttempts: 8, LockoutMinutes: 15}
	case keyRetention:
		return &RetentionSettings{Days: 31, HostStorageWarnMB: 512, DiskWarnPercent: 85, AlertHistoryDays: 180, AuditDays: 365, JobHistoryDays: 180, SecurityEventDays: 30}
	case keyAgent:
		return &AgentDefaults{IntervalSeconds: 30, InventoryIntervalSec: 3600, TopProcesses: 10, CollectServices: true, CollectPorts: true,
			CollectDocker: true, CollectTemperatures: true, BruteforceEnabled: true, SecurityIntervalSec: 60, Fail2banEnabled: true,
			BruteforceWindowMin: 5, AutoBlockMinutes: 60, AutoBlockThreshold: 20}
	case keyAlerting:
		return &AlertingSettings{GroupWaitSeconds: 30, DefaultRepeatMinutes: 240, FlapWindowMinutes: 30, FlapThreshold: 6, HostDownSeconds: 120, ResolveCount: 1}
	case keyVuln:
		return &VulnSettings{Enabled: true, SyncHour: 2, ScanAfterInventory: true, AlertSeverity: "Critical", OnlyFixable: true}
	case keyBranding:
		return &Branding{ProductName: "Heartbeat", AccentColor: "#e11d48"}
	}
	return nil
}

// SettingsStore caches settings rows.
type SettingsStore struct {
	db *db.DB
	mu sync.RWMutex
	c  map[string][]byte // "<tenant>/<key>" -> json
}

func newSettingsStore(d *db.DB) *SettingsStore {
	return &SettingsStore{db: d, c: map[string][]byte{}}
}

func ck(tenant int64, key string) string { return itoa(tenant) + "/" + key }

// raw returns the stored JSON for a key, or nil.
func (s *SettingsStore) raw(tenant int64, key string) ([]byte, error) {
	s.mu.RLock()
	b, ok := s.c[ck(tenant, key)]
	s.mu.RUnlock()
	if ok {
		return b, nil
	}
	var v string
	err := s.db.Get(&v, `SELECT value FROM settings WHERE tenant_id=? AND key=?`, tenant, key)
	if errors.Is(err, sql.ErrNoRows) {
		b = nil
	} else if err != nil {
		return nil, err
	} else {
		b = []byte(v)
	}
	s.mu.Lock()
	s.c[ck(tenant, key)] = b
	s.mu.Unlock()
	return b, nil
}

// Load decodes a section into dst (which should hold defaults already).
func (s *SettingsStore) Load(tenant int64, key string, dst any) error {
	b, err := s.raw(tenant, key)
	if err != nil || b == nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// Save stores a section.
func (s *SettingsStore) Save(ctx context.Context, tenant int64, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings (tenant_id, key, value, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(tenant_id, key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, tenant, key, string(b), db.Now())
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.c[ck(tenant, key)] = b
	s.mu.Unlock()
	return nil
}

// Delete removes a tenant override.
func (s *SettingsStore) Delete(ctx context.Context, tenant int64, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE tenant_id=? AND key=?`, tenant, key)
	s.mu.Lock()
	delete(s.c, ck(tenant, key))
	s.mu.Unlock()
	return err
}

func get[T any](s *SettingsStore, key string) T {
	d := defaultFor(key).(*T)
	_ = s.Load(0, key, d)
	return *d
}

func (s *SettingsStore) General() GeneralSettings     { return get[GeneralSettings](s, keyGeneral) }
func (s *SettingsStore) TLS() TLSSettings             { return get[TLSSettings](s, keyTLS) }
func (s *SettingsStore) SMTP() SMTPSettings           { return get[SMTPSettings](s, keySMTP) }
func (s *SettingsStore) LDAP() LDAPSettings           { return get[LDAPSettings](s, keyLDAP) }
func (s *SettingsStore) RADIUS() RADIUSSettings       { return get[RADIUSSettings](s, keyRADIUS) }
func (s *SettingsStore) Security() SecuritySettings   { return get[SecuritySettings](s, keySecurity) }
func (s *SettingsStore) Retention() RetentionSettings { return get[RetentionSettings](s, keyRetention) }
func (s *SettingsStore) Agent() AgentDefaults         { return get[AgentDefaults](s, keyAgent) }
func (s *SettingsStore) Alerting() AlertingSettings   { return get[AlertingSettings](s, keyAlerting) }
func (s *SettingsStore) Vuln() VulnSettings           { return get[VulnSettings](s, keyVuln) }

// Branding returns the effective branding for a tenant: tenant fields
// override the global (tenant 0) values when set.
func (s *SettingsStore) Branding(tenant int64) Branding {
	b := get[Branding](s, keyBranding)
	if tenant > 0 {
		var t Branding
		if err := s.Load(tenant, keyBranding, &t); err == nil {
			if t.ProductName != "" {
				b.ProductName = t.ProductName
			}
			if t.LogoDataURL != "" {
				b.LogoDataURL = t.LogoDataURL
			}
			if t.FaviconDataURL != "" {
				b.FaviconDataURL = t.FaviconDataURL
			}
			if t.AccentColor != "" {
				b.AccentColor = t.AccentColor
			}
			if t.LoginText != "" {
				b.LoginText = t.LoginText
			}
			if t.FooterText != "" {
				b.FooterText = t.FooterText
			}
		}
	}
	return b
}
