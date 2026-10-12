package agent

import (
	"net"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	pb "heartbeat/gen/heartbeatv1"
)

// failureCounter aggregates failed logins per source IP for one window.
// SSH logs several lines per attempt ("Invalid user", "Failed password",
// "Connection closed ... [preauth]"); attempts are keyed by IP and source
// port so each connection counts its failures once.
type failureCounter struct {
	mu    sync.Mutex
	start time.Time
	conns map[string]*connAttempt // ip|port
	other map[string]*FailedIP    // events without a port (Windows, PAM)
}

type connAttempt struct {
	ip       string
	failures int
	invalid  bool
	aborted  bool
	users    map[string]bool
	last     time.Time
}

// FailedIP is an aggregate for reports.
type FailedIP struct {
	IP    string
	Count int
	Users map[string]bool
	Last  time.Time
}

func newFailureCounter() *failureCounter {
	return &failureCounter{start: time.Now(), conns: map[string]*connAttempt{}, other: map[string]*FailedIP{}}
}

var (
	reFailed  = regexp.MustCompile(`Failed (?:password|publickey|keyboard-interactive/pam|none) for (?:invalid user )?(\S*) from ([0-9a-fA-F:.]+) port (\d+)`)
	reInvalid = regexp.MustCompile(`Invalid user (\S*) from ([0-9a-fA-F:.]+) port (\d+)`)
	reAborted = regexp.MustCompile(`Connection (?:closed|reset) by (?:authenticating|invalid) user (\S*) ([0-9a-fA-F:.]+) port (\d+) \[preauth\]`)
	reMaxAuth = regexp.MustCompile(`maximum authentication attempts exceeded for (?:invalid user )?(\S*) from ([0-9a-fA-F:.]+) port (\d+)`)
)

// ParseAuthLine feeds one syslog/journal line (Linux sshd).
func (f *failureCounter) ParseAuthLine(line string) {
	if !strings.Contains(line, "sshd") && !strings.Contains(line, "Failed") && !strings.Contains(line, "user") {
		return
	}
	now := time.Now()
	try := func(re *regexp.Regexp, kind string) bool {
		m := re.FindStringSubmatch(line)
		if m == nil || net.ParseIP(m[2]) == nil {
			return false
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		key := m[2] + "|" + m[3]
		c := f.conns[key]
		if c == nil {
			c = &connAttempt{ip: m[2], users: map[string]bool{}}
			f.conns[key] = c
		}
		c.last = now
		if m[1] != "" && len(c.users) < 10 {
			c.users[m[1]] = true
		}
		switch kind {
		case "failed":
			c.failures++
		case "invalid":
			c.invalid = true
		case "aborted":
			c.aborted = true
		case "max":
			if c.failures < 1 {
				c.failures = 1
			}
		}
		return true
	}
	_ = try(reFailed, "failed") || try(reInvalid, "invalid") || try(reAborted, "aborted") || try(reMaxAuth, "max")
}

// Add records failures that have no port (e.g. Windows event 4625).
func (f *failureCounter) Add(ip, user string, n int) {
	if net.ParseIP(ip) == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.other[ip]
	if e == nil {
		e = &FailedIP{IP: ip, Users: map[string]bool{}}
		f.other[ip] = e
	}
	e.Count += n
	e.Last = time.Now()
	if user != "" && len(e.Users) < 10 {
		e.Users[user] = true
	}
}

// Flush returns the window's aggregates and starts a new window.
func (f *failureCounter) Flush() (time.Time, time.Time, []*pb.FailedLogins) {
	f.mu.Lock()
	defer f.mu.Unlock()
	agg := map[string]*FailedIP{}
	for ip, e := range f.other {
		agg[ip] = e
	}
	for _, c := range f.conns {
		n := c.failures
		if n == 0 && (c.invalid || c.aborted) {
			n = 1
		}
		if n == 0 {
			continue
		}
		e := agg[c.ip]
		if e == nil {
			e = &FailedIP{IP: c.ip, Users: map[string]bool{}}
			agg[c.ip] = e
		}
		e.Count += n
		if c.last.After(e.Last) {
			e.Last = c.last
		}
		for u := range c.users {
			if len(e.Users) < 10 {
				e.Users[u] = true
			}
		}
	}
	start, end := f.start, time.Now()
	f.start = end
	f.conns = map[string]*connAttempt{}
	f.other = map[string]*FailedIP{}
	var out []*pb.FailedLogins
	for _, e := range agg {
		fl := &pb.FailedLogins{Ip: e.IP, Count: uint32(e.Count), LastSeenMs: e.Last.UnixMilli()}
		for u := range e.Users {
			fl.Usernames = append(fl.Usernames, u)
		}
		sort.Strings(fl.Usernames)
		out = append(out, fl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return start, end, out
}
