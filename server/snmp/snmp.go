// Package snmp polls devices over SNMP v2c/v3 using YAML device profiles.
package snmp

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/gosnmp/gosnmp"
	"gopkg.in/yaml.v3"
)

//go:embed profiles/*.yaml
var builtin embed.FS

// Config describes how to reach a device.
type Config struct {
	Version       string `json:"version"` // 2c, 3
	Community     string `json:"community,omitempty"`
	Port          int    `json:"port"`
	TimeoutSec    int    `json:"timeout_seconds"`
	Retries       int    `json:"retries"`
	Username      string `json:"username,omitempty"`
	SecurityLevel string `json:"security_level,omitempty"` // noAuthNoPriv, authNoPriv, authPriv
	AuthProtocol  string `json:"auth_protocol,omitempty"`  // MD5, SHA, SHA224, SHA256, SHA384, SHA512
	AuthPassword  string `json:"auth_password,omitempty"`
	PrivProtocol  string `json:"priv_protocol,omitempty"` // DES, AES, AES192, AES256, AES192C, AES256C
	PrivPassword  string `json:"priv_password,omitempty"`
	ContextName   string `json:"context_name,omitempty"`
	Profile       string `json:"profile"` // "auto" or a profile name
	IntervalSec   int    `json:"interval_seconds"`
	PollLLDP      *bool  `json:"poll_lldp,omitempty"`
	ExtraMetrics  []MetricDef `json:"extra_metrics,omitempty"` // OIDs added from the UI
}

// SecretFields are stripped from API responses.
var SecretFields = []string{"community", "auth_password", "priv_password"}

// Profile is a device profile loaded from YAML.
type Profile struct {
	Name        string      `yaml:"name" json:"name"`
	Description string      `yaml:"description" json:"description"`
	Include     []string    `yaml:"include" json:"include,omitempty"`
	Match       Match       `yaml:"match" json:"match"`
	Metrics     []MetricDef `yaml:"metrics" json:"metrics"`
	Source      string      `yaml:"-" json:"source"`
}

// Match selects a profile by sysObjectID prefix or sysDescr regex.
type Match struct {
	SysObjectID []string `yaml:"sys_object_id" json:"sys_object_id,omitempty"`
	SysDescr    []string `yaml:"sys_descr" json:"sys_descr,omitempty"`
}

// MetricDef defines one metric. Either OID (shorthand for expr "v") or
// Expr + Vars. Vars ending in .0 are scalars (GET) broadcast to every index;
// others are walked tables evaluated per index.
type MetricDef struct {
	Name      string            `yaml:"name" json:"name"`
	OID       string            `yaml:"oid,omitempty" json:"oid,omitempty"`
	Expr      string            `yaml:"expr,omitempty" json:"expr,omitempty"`
	Vars      map[string]string `yaml:"vars,omitempty" json:"vars,omitempty"`
	Filter    *Filter           `yaml:"filter,omitempty" json:"filter,omitempty"`
	Aggregate string            `yaml:"aggregate,omitempty" json:"aggregate,omitempty"` // avg, sum, min, max, first; ignored with label_oid
	Scale     float64           `yaml:"scale,omitempty" json:"scale,omitempty"`
	LabelOID  string            `yaml:"label_oid,omitempty" json:"label_oid,omitempty"`
	LabelName string            `yaml:"label_name,omitempty" json:"label_name,omitempty"`
	// LabelExclude drops per-index series whose label matches this regex.
	LabelExclude string `yaml:"label_exclude,omitempty" json:"label_exclude,omitempty"`
	// Counters: vars are monotonically increasing counters; the expression
	// sees per-second rates between consecutive polls (needs two polls).
	Counters bool `yaml:"counters,omitempty" json:"counters,omitempty"`
}

// Filter keeps only table indices whose value at OID equals/matches.
type Filter struct {
	OID    string `yaml:"oid" json:"oid"`
	Equals string `yaml:"equals,omitempty" json:"equals,omitempty"`
	Regex  string `yaml:"regex,omitempty" json:"regex,omitempty"`
}

// System holds the MIB-2 system group.
type System struct {
	Descr    string  `json:"descr"`
	ObjectID string  `json:"object_id"`
	Name     string  `json:"name"`
	Location string  `json:"location"`
	Contact  string  `json:"contact"`
	Uptime   float64 `json:"uptime_seconds"`
}

// Interface is one row of IF-MIB.
type Interface struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	Descr       string `json:"descr"`
	Alias       string `json:"alias"`
	Type        int    `json:"type"`
	SpeedBps    uint64 `json:"speed_bps"`
	MAC         string `json:"mac"`
	AdminUp     bool   `json:"admin_up"`
	OperUp      bool   `json:"oper_up"`
	InOctets    uint64 `json:"-"`
	OutOctets   uint64 `json:"-"`
	HC          bool   `json:"-"`
	InErrors    uint64 `json:"-"`
	OutErrors   uint64 `json:"-"`
	InDiscards  uint64 `json:"-"`
	OutDiscards uint64 `json:"-"`
	// Filled by the caller from counter deltas.
	InBps   float64 `json:"in_bps"`
	OutBps  float64 `json:"out_bps"`
	UtilPct float64 `json:"util_percent"`
}

// Neighbor is an LLDP remote entry.
type Neighbor struct {
	LocalPort      string `json:"local_port"`
	RemoteChassis  string `json:"remote_chassis"`
	RemotePort     string `json:"remote_port"`
	RemotePortDesc string `json:"remote_port_desc"`
	RemoteSysName  string `json:"remote_sysname"`
	RemoteSysDesc  string `json:"remote_sysdesc"`
	MgmtAddr       string `json:"mgmt_addr"`
}

// Metric is a profile metric value.
type Metric struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// Result of a poll.
type Result struct {
	System     System      `json:"system"`
	Profile    string      `json:"profile"`
	Interfaces []Interface `json:"interfaces"`
	Metrics    []Metric    `json:"-"`
	Neighbors  []Neighbor  `json:"neighbors"`
	Errors     []string    `json:"errors,omitempty"`
	DurationMs float64     `json:"duration_ms"`
}

// LoadProfiles loads built-in profiles plus *.yaml in extraDir (which may
// override built-ins by name).
func LoadProfiles(extraDir string) (map[string]*Profile, error) {
	out := map[string]*Profile{}
	load := func(fsys fs.FS, dir, source string) error {
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() || !(strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml")) {
				continue
			}
			b, err := fs.ReadFile(fsys, filepath.ToSlash(filepath.Join(dir, e.Name())))
			if err != nil {
				return err
			}
			var p Profile
			if err := yaml.Unmarshal(b, &p); err != nil {
				return fmt.Errorf("%s: %w", e.Name(), err)
			}
			if p.Name == "" {
				p.Name = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			}
			if err := p.Validate(); err != nil {
				return fmt.Errorf("%s: %w", e.Name(), err)
			}
			p.Source = source
			out[p.Name] = &p
		}
		return nil
	}
	if err := load(builtin, "profiles", "builtin"); err != nil {
		return nil, err
	}
	if extraDir != "" {
		if _, err := os.Stat(extraDir); err == nil {
			if err := load(os.DirFS(extraDir), ".", "custom"); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// Validate compiles expressions and checks OIDs.
func (p *Profile) Validate() error {
	for _, m := range p.Metrics {
		if err := m.validate(); err != nil {
			return fmt.Errorf("metric %s: %w", m.Name, err)
		}
	}
	return nil
}

var oidRe = regexp.MustCompile(`^\.?[0-9]+(\.[0-9]+)+$`)

func (m MetricDef) validate() error {
	if m.Name == "" {
		return errors.New("name required")
	}
	if m.OID == "" && m.Expr == "" {
		return errors.New("oid or expr required")
	}
	if m.OID != "" && !oidRe.MatchString(m.OID) {
		return fmt.Errorf("invalid oid %q", m.OID)
	}
	for k, v := range m.Vars {
		if !oidRe.MatchString(v) {
			return fmt.Errorf("var %s: invalid oid %q", k, v)
		}
	}
	if m.Expr != "" {
		env := map[string]any{}
		for k := range m.Vars {
			env[k] = 1.0
		}
		if _, err := expr.Compile(m.Expr, expr.Env(env), expr.AsFloat64()); err != nil {
			return fmt.Errorf("expr: %w", err)
		}
	}
	return nil
}

// ValidateMetric is exported for UI-added OIDs.
func ValidateMetric(m MetricDef) error { return m.validate() }

// expand resolves includes.
func expand(p *Profile, all map[string]*Profile, seen map[string]bool) []MetricDef {
	if seen[p.Name] {
		return nil
	}
	seen[p.Name] = true
	var ms []MetricDef
	for _, inc := range p.Include {
		if ip, ok := all[inc]; ok {
			ms = append(ms, expand(ip, all, seen)...)
		}
	}
	// A profile's own definition takes precedence over an included one with
	// the same name, but the included one stays as a fallback (Poll uses the
	// first definition of each name that returns data).
	return append(append([]MetricDef{}, p.Metrics...), ms...)
}

// Select chooses a profile for a device.
func Select(profiles map[string]*Profile, name string, sys System) *Profile {
	if name != "" && name != "auto" {
		if p, ok := profiles[name]; ok {
			return p
		}
	}
	names := make([]string, 0, len(profiles))
	for n := range profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	oid := strings.TrimPrefix(sys.ObjectID, ".")
	for _, n := range names {
		p := profiles[n]
		for _, pre := range p.Match.SysObjectID {
			if strings.HasPrefix(oid, strings.TrimPrefix(pre, ".")) {
				return p
			}
		}
	}
	for _, n := range names {
		p := profiles[n]
		for _, re := range p.Match.SysDescr {
			if r, err := regexp.Compile("(?i)" + re); err == nil && r.MatchString(sys.Descr) {
				return p
			}
		}
	}
	return profiles["generic"]
}

func connect(host string, cfg Config) (*gosnmp.GoSNMP, error) {
	port := cfg.Port
	if port == 0 {
		port = 161
	}
	to := time.Duration(cfg.TimeoutSec) * time.Second
	if to <= 0 {
		to = 5 * time.Second
	}
	g := &gosnmp.GoSNMP{Target: host, Port: uint16(port), Timeout: to, Retries: cfg.Retries, MaxOids: 40, MaxRepetitions: 25}
	switch cfg.Version {
	case "3":
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		g.ContextName = cfg.ContextName
		usm := &gosnmp.UsmSecurityParameters{UserName: cfg.Username}
		switch strings.ToLower(cfg.SecurityLevel) {
		case "authpriv":
			g.MsgFlags = gosnmp.AuthPriv
		case "authnopriv":
			g.MsgFlags = gosnmp.AuthNoPriv
		default:
			g.MsgFlags = gosnmp.NoAuthNoPriv
		}
		if g.MsgFlags == gosnmp.AuthNoPriv || g.MsgFlags == gosnmp.AuthPriv {
			usm.AuthenticationPassphrase = cfg.AuthPassword
			switch strings.ToUpper(cfg.AuthProtocol) {
			case "MD5":
				usm.AuthenticationProtocol = gosnmp.MD5
			case "SHA224":
				usm.AuthenticationProtocol = gosnmp.SHA224
			case "SHA256":
				usm.AuthenticationProtocol = gosnmp.SHA256
			case "SHA384":
				usm.AuthenticationProtocol = gosnmp.SHA384
			case "SHA512":
				usm.AuthenticationProtocol = gosnmp.SHA512
			default:
				usm.AuthenticationProtocol = gosnmp.SHA
			}
		}
		if g.MsgFlags == gosnmp.AuthPriv {
			usm.PrivacyPassphrase = cfg.PrivPassword
			switch strings.ToUpper(cfg.PrivProtocol) {
			case "DES":
				usm.PrivacyProtocol = gosnmp.DES
			case "AES192":
				usm.PrivacyProtocol = gosnmp.AES192
			case "AES256":
				usm.PrivacyProtocol = gosnmp.AES256
			case "AES192C":
				usm.PrivacyProtocol = gosnmp.AES192C
			case "AES256C":
				usm.PrivacyProtocol = gosnmp.AES256C
			default:
				usm.PrivacyProtocol = gosnmp.AES
			}
		}
		g.SecurityParameters = usm
	default:
		g.Version = gosnmp.Version2c
		g.Community = cfg.Community
		if g.Community == "" {
			g.Community = "public"
		}
	}
	if err := g.Connect(); err != nil {
		return nil, err
	}
	return g, nil
}

// table maps index suffix -> value.
type table map[string]gosnmp.SnmpPDU

func walk(g *gosnmp.GoSNMP, oid string) (table, error) {
	oid = "." + strings.TrimPrefix(oid, ".")
	t := table{}
	err := g.BulkWalk(oid, func(p gosnmp.SnmpPDU) error {
		if p.Type == gosnmp.NoSuchObject || p.Type == gosnmp.NoSuchInstance || p.Type == gosnmp.EndOfMibView {
			return nil
		}
		idx := strings.TrimPrefix(strings.TrimPrefix(p.Name, oid), ".")
		t[idx] = p
		return nil
	})
	return t, err
}

func get(g *gosnmp.GoSNMP, oids []string) (map[string]gosnmp.SnmpPDU, error) {
	out := map[string]gosnmp.SnmpPDU{}
	for i := 0; i < len(oids); i += 20 {
		end := i + 20
		if end > len(oids) {
			end = len(oids)
		}
		res, err := g.Get(oids[i:end])
		if err != nil {
			return out, err
		}
		for _, p := range res.Variables {
			if p.Type == gosnmp.NoSuchObject || p.Type == gosnmp.NoSuchInstance || p.Type == gosnmp.Null {
				continue
			}
			out[strings.TrimPrefix(p.Name, ".")] = p
		}
	}
	return out, nil
}

func pduString(p gosnmp.SnmpPDU) string {
	switch v := p.Value.(type) {
	case []byte:
		if printable(v) {
			return strings.TrimRight(string(v), "\x00")
		}
		return hexColon(v)
	case string:
		return v
	case nil:
		return ""
	}
	if p.Type == gosnmp.ObjectIdentifier {
		return strings.TrimPrefix(fmt.Sprint(p.Value), ".")
	}
	return gosnmp.ToBigInt(p.Value).String()
}

func printable(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, c := range strings.TrimRight(string(b), "\x00") {
		if !unicode.IsPrint(c) && !unicode.IsSpace(c) {
			return false
		}
	}
	return true
}

func hexColon(b []byte) string {
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = fmt.Sprintf("%02x", c)
	}
	return strings.Join(parts, ":")
}

func pduFloat(p gosnmp.SnmpPDU) (float64, bool) {
	switch v := p.Value.(type) {
	case []byte:
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimRight(string(v), "\x00")), 64)
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	case nil:
		return 0, false
	}
	bi := gosnmp.ToBigInt(p.Value)
	f, _ := new(big.Float).SetInt(bi).Float64()
	return f, true
}

func pduUint(p gosnmp.SnmpPDU) uint64 {
	if p.Value == nil {
		return 0
	}
	if _, ok := p.Value.([]byte); ok {
		return 0
	}
	return gosnmp.ToBigInt(p.Value).Uint64()
}

const (
	oidSysDescr    = "1.3.6.1.2.1.1.1.0"
	oidSysObjectID = "1.3.6.1.2.1.1.2.0"
	oidSysUpTime   = "1.3.6.1.2.1.1.3.0"
	oidSysContact  = "1.3.6.1.2.1.1.4.0"
	oidSysName     = "1.3.6.1.2.1.1.5.0"
	oidSysLocation = "1.3.6.1.2.1.1.6.0"
	oidHrUptime    = "1.3.6.1.2.1.25.1.1.0" // hrSystemUptime: host uptime, not the SNMP agent's
)

// Probe fetches only the system group (connectivity test / discovery).
func Probe(ctx context.Context, host string, cfg Config) (*System, error) {
	g, err := connect(host, cfg)
	if err != nil {
		return nil, err
	}
	defer g.Conn.Close()
	return system(g)
}

func system(g *gosnmp.GoSNMP) (*System, error) {
	v, err := get(g, []string{oidSysDescr, oidSysObjectID, oidSysUpTime, oidSysContact, oidSysName, oidSysLocation, oidHrUptime})
	if err != nil {
		return nil, err
	}
	if len(v) == 0 {
		return nil, errors.New("no response to system group")
	}
	s := &System{Descr: pduString(v[oidSysDescr]), ObjectID: pduString(v[oidSysObjectID]), Name: pduString(v[oidSysName]),
		Location: pduString(v[oidSysLocation]), Contact: pduString(v[oidSysContact])}
	if t, ok := pduFloat(v[oidSysUpTime]); ok {
		s.Uptime = t / 100
	}
	if t, ok := pduFloat(v[oidHrUptime]); ok && t > 0 {
		s.Uptime = t / 100
	}
	return s, nil
}

// Poll collects system info, interfaces, profile metrics and LLDP.
func Poll(ctx context.Context, host string, cfg Config, profiles map[string]*Profile, st *State) (*Result, error) {
	start := time.Now()
	g, err := connect(host, cfg)
	if err != nil {
		return nil, err
	}
	defer g.Conn.Close()
	sys, err := system(g)
	if err != nil {
		return nil, err
	}
	r := &Result{System: *sys}
	if ifs, err := interfaces(g); err != nil {
		r.Errors = append(r.Errors, "interfaces: "+err.Error())
	} else {
		r.Interfaces = ifs
	}
	p := Select(profiles, cfg.Profile, *sys)
	defs := cfg.ExtraMetrics
	if p != nil {
		r.Profile = p.Name
		defs = append(expand(p, profiles, map[string]bool{}), cfg.ExtraMetrics...)
	}
	cache := map[string]table{}
	done := map[string]bool{}
	failed := map[string]string{}
	for _, d := range defs {
		if done[d.Name] {
			continue // a more specific definition already produced data
		}
		ms, err := evalMetric(g, d, cache, st, host)
		if err != nil || len(ms) == 0 {
			if _, seen := failed[d.Name]; !seen && err != nil {
				failed[d.Name] = err.Error()
			}
			continue
		}
		done[d.Name] = true
		r.Metrics = append(r.Metrics, ms...)
	}
	for name, e := range failed {
		if !done[name] {
			r.Errors = append(r.Errors, name+": "+e)
		}
	}
	sort.Strings(r.Errors)
	if cfg.PollLLDP == nil || *cfg.PollLLDP {
		if n, err := lldp(g, r.Interfaces); err == nil {
			r.Neighbors = n
		}
	}
	r.DurationMs = float64(time.Since(start).Microseconds()) / 1000
	return r, nil
}

func interfaces(g *gosnmp.GoSNMP) ([]Interface, error) {
	cols := map[string]string{
		"descr": "1.3.6.1.2.1.2.2.1.2", "type": "1.3.6.1.2.1.2.2.1.3", "speed": "1.3.6.1.2.1.2.2.1.5", "mac": "1.3.6.1.2.1.2.2.1.6",
		"admin": "1.3.6.1.2.1.2.2.1.7", "oper": "1.3.6.1.2.1.2.2.1.8", "inoct": "1.3.6.1.2.1.2.2.1.10", "indisc": "1.3.6.1.2.1.2.2.1.13",
		"inerr": "1.3.6.1.2.1.2.2.1.14", "outoct": "1.3.6.1.2.1.2.2.1.16", "outdisc": "1.3.6.1.2.1.2.2.1.19", "outerr": "1.3.6.1.2.1.2.2.1.20",
		"name": "1.3.6.1.2.1.31.1.1.1.1", "hcin": "1.3.6.1.2.1.31.1.1.1.6", "hcout": "1.3.6.1.2.1.31.1.1.1.10", "highspeed": "1.3.6.1.2.1.31.1.1.1.15",
		"alias": "1.3.6.1.2.1.31.1.1.1.18",
	}
	t := map[string]table{}
	for k, oid := range cols {
		tb, err := walk(g, oid)
		if err != nil && k == "descr" {
			return nil, err
		}
		t[k] = tb
	}
	var out []Interface
	for idx, d := range t["descr"] {
		i, _ := strconv.Atoi(idx)
		it := Interface{Index: i, Descr: pduString(d), Name: pduString(t["name"][idx]), Alias: pduString(t["alias"][idx]),
			MAC: macString(t["mac"][idx]), AdminUp: pduUint(t["admin"][idx]) == 1, OperUp: pduUint(t["oper"][idx]) == 1}
		it.Type = int(pduUint(t["type"][idx]))
		if it.Name == "" {
			it.Name = it.Descr
		}
		if hs := pduUint(t["highspeed"][idx]); hs > 0 {
			it.SpeedBps = hs * 1_000_000
		} else {
			it.SpeedBps = pduUint(t["speed"][idx])
		}
		if p, ok := t["hcin"][idx]; ok {
			it.InOctets, it.OutOctets, it.HC = pduUint(p), pduUint(t["hcout"][idx]), true
		} else {
			it.InOctets, it.OutOctets = pduUint(t["inoct"][idx]), pduUint(t["outoct"][idx])
		}
		it.InErrors, it.OutErrors = pduUint(t["inerr"][idx]), pduUint(t["outerr"][idx])
		it.InDiscards, it.OutDiscards = pduUint(t["indisc"][idx]), pduUint(t["outdisc"][idx])
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

func macString(p gosnmp.SnmpPDU) string {
	if b, ok := p.Value.([]byte); ok && len(b) == 6 {
		return hexColon(b)
	}
	return ""
}

func isScalar(oid string) bool { return strings.HasSuffix(oid, ".0") }

func fetch(g *gosnmp.GoSNMP, oid string, cache map[string]table) (table, error) {
	oid = strings.TrimPrefix(oid, ".")
	if t, ok := cache[oid]; ok {
		return t, nil
	}
	var t table
	if isScalar(oid) {
		v, err := get(g, []string{oid})
		if err != nil {
			return nil, err
		}
		t = table{}
		if p, ok := v[oid]; ok {
			t[""] = p
		}
	} else {
		var err error
		if t, err = walk(g, oid); err != nil {
			return nil, err
		}
	}
	cache[oid] = t
	return t, nil
}

var programCache sync.Map // string -> *vm.Program

// State remembers counter values between polls for `counters: true` metrics.
type State struct {
	mu sync.Mutex
	m  map[string]counterVal
}

type counterVal struct {
	v float64
	t time.Time
}

// NewState creates counter state.
func NewState() *State { return &State{m: map[string]counterVal{}} }

// rate records v and returns the per-second rate since the previous value.
func (s *State) rate(key string, v float64, c32 bool) (float64, bool) {
	if s == nil {
		return 0, false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.m[key]
	s.m[key] = counterVal{v, now}
	dt := now.Sub(prev.t).Seconds()
	if !ok || dt <= 0 {
		return 0, false
	}
	d := v - prev.v
	if d < 0 {
		if !c32 {
			return 0, false // reset
		}
		d += 1 << 32
	}
	return d / dt, true
}

// Forget drops state for a host.
func (s *State) Forget(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.m {
		if strings.HasPrefix(k, host+"|") {
			delete(s.m, k)
		}
	}
}

func evalMetric(g *gosnmp.GoSNMP, d MetricDef, cache map[string]table, st *State, host string) ([]Metric, error) {
	vars := d.Vars
	ex := d.Expr
	if d.OID != "" {
		vars = map[string]string{"v": d.OID}
		if ex == "" {
			ex = "v"
		}
	}
	tables := map[string]table{}
	var indexSet map[string]bool
	for name, oid := range vars {
		t, err := fetch(g, oid, cache)
		if err != nil {
			return nil, err
		}
		tables[name] = t
		if !isScalar(oid) {
			if indexSet == nil {
				indexSet = map[string]bool{}
				for k := range t {
					indexSet[k] = true
				}
			} else {
				for k := range indexSet {
					if _, ok := t[k]; !ok {
						delete(indexSet, k)
					}
				}
			}
		}
	}
	if indexSet == nil {
		indexSet = map[string]bool{"": true}
	}
	if d.Filter != nil {
		ft, err := fetch(g, d.Filter.OID, cache)
		if err != nil {
			return nil, err
		}
		var re *regexp.Regexp
		if d.Filter.Regex != "" {
			re, _ = regexp.Compile(d.Filter.Regex)
		}
		for k := range indexSet {
			v := pduString(ft[k])
			keep := (d.Filter.Equals != "" && strings.TrimPrefix(v, ".") == strings.TrimPrefix(d.Filter.Equals, ".")) || (re != nil && re.MatchString(v))
			if !keep {
				delete(indexSet, k)
			}
		}
	}
	key := ex + "|" + strings.Join(sortedKeys(vars), ",")
	var prog *vm.Program
	if p, ok := programCache.Load(key); ok {
		prog = p.(*vm.Program)
	} else {
		env := map[string]any{}
		for k := range vars {
			env[k] = 1.0
		}
		p, err := expr.Compile(ex, expr.Env(env), expr.AsFloat64())
		if err != nil {
			return nil, err
		}
		programCache.Store(key, p)
		prog = p
	}
	var labels table
	if d.LabelOID != "" {
		labels, _ = fetch(g, d.LabelOID, cache)
	}
	scale := d.Scale
	if scale == 0 {
		scale = 1
	}
	var values []float64
	var out []Metric
	idxs := make([]string, 0, len(indexSet))
	for k := range indexSet {
		idxs = append(idxs, k)
	}
	sort.Strings(idxs)
	for _, idx := range idxs {
		env := map[string]any{}
		ok := true
		for name, oid := range vars {
			k := idx
			if isScalar(oid) {
				k = ""
			}
			p := tables[name][k]
			f, good := pduFloat(p)
			if !good {
				ok = false
				break
			}
			if d.Counters {
				r, have := st.rate(host+"|"+oid+"|"+k, f, p.Type == gosnmp.Counter32)
				if !have {
					ok = false // first sample: keep recording the other vars
					continue
				}
				f = r
			}
			env[name] = f
		}
		if !ok {
			continue
		}
		res, err := expr.Run(prog, env)
		if err != nil {
			continue
		}
		f, _ := res.(float64)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		f *= scale
		if labels != nil {
			ln := d.LabelName
			if ln == "" {
				ln = "index"
			}
			lv := pduString(labels[idx])
			if lv == "" {
				lv = idx
			}
			if d.LabelExclude != "" {
				if re, err := regexp.Compile(d.LabelExclude); err == nil && re.MatchString(lv) {
					continue
				}
			}
			out = append(out, Metric{Name: d.Name, Labels: map[string]string{ln: lv}, Value: f})
		} else {
			values = append(values, f)
		}
	}
	if labels != nil {
		return out, nil
	}
	if len(values) == 0 {
		return nil, errors.New("no data")
	}
	var v float64
	switch d.Aggregate {
	case "sum":
		for _, x := range values {
			v += x
		}
	case "max":
		v = values[0]
		for _, x := range values {
			v = math.Max(v, x)
		}
	case "min":
		v = values[0]
		for _, x := range values {
			v = math.Min(v, x)
		}
	case "first":
		v = values[0]
	default:
		for _, x := range values {
			v += x
		}
		v /= float64(len(values))
	}
	return []Metric{{Name: d.Name, Value: v}}, nil
}

func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x+"="+m[x])
	}
	sort.Strings(k)
	return k
}

const (
	lldpRemTable   = "1.0.8802.1.1.2.1.4.1.1"
	lldpLocPortID  = "1.0.8802.1.1.2.1.3.7.1.3"
	lldpLocPortDsc = "1.0.8802.1.1.2.1.3.7.1.4"
	lldpRemManAddr = "1.0.8802.1.1.2.1.4.2.1.3"
)

func lldp(g *gosnmp.GoSNMP, ifs []Interface) ([]Neighbor, error) {
	cols := map[string]string{"chassis": lldpRemTable + ".5", "port": lldpRemTable + ".7", "portdesc": lldpRemTable + ".8",
		"sysname": lldpRemTable + ".9", "sysdesc": lldpRemTable + ".10"}
	t := map[string]table{}
	for k, oid := range cols {
		tb, err := walk(g, oid)
		if err != nil {
			return nil, err
		}
		t[k] = tb
	}
	if len(t["chassis"]) == 0 {
		return nil, nil
	}
	locID, _ := walk(g, lldpLocPortID)
	locDesc, _ := walk(g, lldpLocPortDsc)
	mgmt, _ := walk(g, lldpRemManAddr)
	ifName := map[string]string{}
	for _, i := range ifs {
		ifName[strconv.Itoa(i.Index)] = i.Name
	}
	var out []Neighbor
	for idx, ch := range t["chassis"] {
		// index = timeMark.localPortNum.remIndex
		parts := strings.Split(idx, ".")
		if len(parts) < 3 {
			continue
		}
		lp := parts[1]
		local := pduString(locDesc[lp])
		if local == "" {
			local = pduString(locID[lp])
		}
		if local == "" {
			local = ifName[lp]
		}
		if local == "" {
			local = "port " + lp
		}
		n := Neighbor{LocalPort: local, RemoteChassis: pduString(ch), RemotePort: pduString(t["port"][idx]),
			RemotePortDesc: pduString(t["portdesc"][idx]), RemoteSysName: pduString(t["sysname"][idx]), RemoteSysDesc: pduString(t["sysdesc"][idx])}
		prefix := idx + "."
		for mi := range mgmt {
			if strings.HasPrefix(mi, prefix) {
				// remaining: addrSubtype.addrLen.addr...
				rest := strings.Split(strings.TrimPrefix(mi, prefix), ".")
				if len(rest) >= 6 && rest[0] == "1" && rest[1] == "4" {
					n.MgmtAddr = strings.Join(rest[2:6], ".")
				}
				break
			}
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LocalPort < out[j].LocalPort })
	return out, nil
}
