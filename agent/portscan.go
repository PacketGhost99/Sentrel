package agent

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "sentrel/gen/sentrelv1"
)

// Port-scan detection is done by the kernel: an nftables chain counts SYNs to
// ports nobody listens on, per source, and adds sources over the threshold to
// a timed set. The agent only keeps the "open ports" set current and reads the
// scanner set back every few seconds.

const (
	// scanTimeoutSec is how long a source stays flagged after its last probe.
	scanTimeoutSec = 600
	// scanDefaultThreshold is the closed-port SYNs per minute that flag a source.
	scanDefaultThreshold = 20
	// scanMaxThreshold bounds the configurable threshold.
	scanMaxThreshold = 100000
	// scanReReport is how often a still-active scanner is reported again.
	scanReReport = 5 * time.Minute
	// scanMaxTracked and scanMaxPerReport bound agent memory and report size.
	scanMaxTracked   = 2000
	scanMaxPerReport = 100
	// scanMaxAllow bounds the allowlist passed to the helper.
	scanMaxAllow = 256
)

// ScanEntry is one flagged source as printed by `priv scan-list`.
type ScanEntry struct {
	IP        string `json:"ip"`
	Remaining int64  `json:"remaining_sec"`
}

// validPortList parses the PORTS argument of scan-sync: "-" (none) or a
// comma-separated list of ports 1-65535. The result is sorted and unique.
func validPortList(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "-" {
		return nil, nil
	}
	if s == "" || len(s) > 6*65535 {
		return nil, fmt.Errorf("invalid port list")
	}
	seen := map[int]bool{}
	var out []int
	for _, f := range strings.Split(s, ",") {
		if f == "" || len(f) > 5 {
			return nil, fmt.Errorf("invalid port %q", f)
		}
		for _, c := range f {
			if c < '0' || c > '9' {
				return nil, fmt.Errorf("invalid port %q", f)
			}
		}
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid port %q", f)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out, nil
}

// formatPortList is the inverse of validPortList.
func formatPortList(ports []int) string {
	if len(ports) == 0 {
		return "-"
	}
	p := append([]int(nil), ports...)
	sort.Ints(p)
	var b strings.Builder
	last := -1
	for _, n := range p {
		if n == last {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(n))
		last = n
	}
	return b.String()
}

// validAddrList parses a comma-separated list of IPs and CIDRs ("-" is none)
// and returns them in canonical form, split by family.
func validAddrList(s string) (v4, v6 []string, err error) {
	s = strings.TrimSpace(s)
	if s == "-" {
		return nil, nil, nil
	}
	if s == "" {
		return nil, nil, fmt.Errorf("invalid address list")
	}
	parts := strings.Split(s, ",")
	if len(parts) > scanMaxAllow {
		return nil, nil, fmt.Errorf("too many addresses (max %d)", scanMaxAllow)
	}
	seen := map[string]bool{}
	for _, f := range parts {
		f = strings.TrimSpace(f)
		var canon string
		var is4 bool
		if strings.Contains(f, "/") {
			ip, n, e := net.ParseCIDR(f)
			if e != nil {
				return nil, nil, fmt.Errorf("invalid address %q", f)
			}
			canon, is4 = n.String(), ip.To4() != nil
		} else {
			ip := net.ParseIP(f)
			if ip == nil {
				return nil, nil, fmt.Errorf("invalid address %q", f)
			}
			canon, is4 = ip.String(), ip.To4() != nil
		}
		if seen[canon] {
			continue
		}
		seen[canon] = true
		if is4 {
			v4 = append(v4, canon)
		} else {
			v6 = append(v6, canon)
		}
	}
	return v4, v6, nil
}

// validScanThreshold parses the --threshold value.
func validScanThreshold(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > scanMaxThreshold {
		return 0, fmt.Errorf("invalid threshold %q (1-%d per minute)", s, scanMaxThreshold)
	}
	return n, nil
}

// parseScanSet extracts the entries from `nft -j list set` output.
func parseScanSet(b []byte) ([]ScanEntry, error) {
	var doc struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	var out []ScanEntry
	for _, item := range doc.Nftables {
		raw, ok := item["set"]
		if !ok {
			continue
		}
		var s struct {
			Elem []json.RawMessage `json:"elem"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		for _, e := range s.Elem {
			var plain string
			if json.Unmarshal(e, &plain) == nil {
				if net.ParseIP(plain) != nil {
					out = append(out, ScanEntry{IP: plain, Remaining: scanTimeoutSec})
				}
				continue
			}
			var obj struct {
				Elem struct {
					Val     string `json:"val"`
					Expires int64  `json:"expires"`
				} `json:"elem"`
			}
			if json.Unmarshal(e, &obj) == nil && net.ParseIP(obj.Elem.Val) != nil {
				out = append(out, ScanEntry{IP: obj.Elem.Val, Remaining: obj.Elem.Expires})
			}
		}
	}
	return out, nil
}

// scanTracker turns successive scanner-set snapshots into reports: a source
// is reported when first seen and again every scanReReport while it keeps
// probing, never on every poll.
type scanTracker struct {
	mu   sync.Mutex
	seen map[string]*scanSeen
}

type scanSeen struct {
	first, last, reported time.Time
}

func newScanTracker() *scanTracker { return &scanTracker{seen: map[string]*scanSeen{}} }

// Observe consumes a snapshot taken at now and returns the reports to send.
func (t *scanTracker) Observe(now time.Time, entries []ScanEntry) []*pb.PortScan {
	t.mu.Lock()
	defer t.mu.Unlock()
	present := map[string]bool{}
	var out []*pb.PortScan
	for _, e := range entries {
		ip := net.ParseIP(e.IP)
		if ip == nil {
			continue
		}
		key := ip.String()
		present[key] = true
		rem := e.Remaining
		if rem < 0 {
			rem = 0
		}
		if rem > scanTimeoutSec {
			rem = scanTimeoutSec
		}
		// The set refreshes an entry's timeout on every probe, so the time
		// since the last probe is the timeout that has elapsed.
		last := now.Add(-time.Duration(scanTimeoutSec-rem) * time.Second)
		s := t.seen[key]
		if s == nil {
			if len(t.seen) >= scanMaxTracked {
				continue
			}
			s = &scanSeen{first: last, last: last}
			t.seen[key] = s
		} else if last.After(s.last) {
			s.last = last
		}
		if s.reported.IsZero() || s.last.Sub(s.reported) >= scanReReport {
			if len(out) >= scanMaxPerReport {
				continue
			}
			s.reported = s.last
			out = append(out, &pb.PortScan{Ip: key, FirstSeenMs: s.first.UnixMilli(), LastSeenMs: s.last.UnixMilli()})
		}
	}
	for k := range t.seen {
		if !present[k] {
			delete(t.seen, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ip < out[j].Ip })
	return out
}

// portscanState carries what the agent last told the helper, so it only
// calls scan-sync when something changed.
type portscanState struct {
	mu        sync.Mutex
	status    *pb.PortScanStatus // latest status to report
	sentAt    time.Time          // when status was last sent
	pending   []*pb.PortScan     // reports waiting for the next security report
	syncedKey string             // ports+config of the last successful sync
	syncedAt  time.Time
	failAt    time.Time
	failKey   string
}

// scanSyncKey identifies the desired helper state.
func scanSyncKey(ports []int, threshold int, allow, exclude []string) string {
	a := append([]string(nil), allow...)
	x := append([]string(nil), exclude...)
	sort.Strings(a)
	sort.Strings(x)
	return formatPortList(ports) + "|" + strconv.Itoa(threshold) + "|" + strings.Join(a, ",") + "|" + strings.Join(x, ",")
}

// buildScanScript renders the nft batch that (re)creates the detection rules.
// It runs as one atomic transaction: the open-port and allow sets are
// replaced and the chain rebuilt without a window where either is empty.
// The chain only ever counts; every rule falls through and the policy is
// accept, so it can never drop or alter traffic. Blocklist sets and any
// active blocks are not touched.
func buildScanScript(ports []int, threshold int, allow4, allow6 []string) string {
	var b strings.Builder
	b.WriteString(`add table inet sentrel
add set inet sentrel open_tcp { type inet_service; }
add set inet sentrel scanners4 { type ipv4_addr; flags dynamic,timeout; timeout 10m; size 4096; }
add set inet sentrel scanners6 { type ipv6_addr; flags dynamic,timeout; timeout 10m; size 4096; }
add set inet sentrel probe4 { type ipv4_addr; flags dynamic,timeout; timeout 1m; size 16384; }
add set inet sentrel probe6 { type ipv6_addr; flags dynamic,timeout; timeout 1m; size 16384; }
add set inet sentrel allow4 { type ipv4_addr; flags interval; }
add set inet sentrel allow6 { type ipv6_addr; flags interval; }
add chain inet sentrel scan { type filter hook input priority -5; policy accept; }
flush chain inet sentrel scan
flush set inet sentrel open_tcp
flush set inet sentrel allow4
flush set inet sentrel allow6
`)
	if len(ports) > 0 {
		b.WriteString("add element inet sentrel open_tcp { " + strings.ReplaceAll(formatPortList(ports), ",", ", ") + " }\n")
	}
	if len(allow4) > 0 {
		b.WriteString("add element inet sentrel allow4 { " + strings.Join(allow4, ", ") + " }\n")
	}
	if len(allow6) > 0 {
		b.WriteString("add element inet sentrel allow6 { " + strings.Join(allow6, ", ") + " }\n")
	}
	fmt.Fprintf(&b, "add rule inet sentrel scan iif lo return\n")
	for _, f := range []struct{ ip, probe, scan, allow string }{{"ip", "probe4", "scanners4", "allow4"}, {"ip6", "probe6", "scanners6", "allow6"}} {
		fmt.Fprintf(&b, "add rule inet sentrel scan tcp flags & (syn | ack) == syn tcp dport != @open_tcp %s saddr != @%s add @%s { %s saddr limit rate over %d/minute burst %d packets } update @%s { %s saddr timeout 10m }\n",
			f.ip, f.allow, f.probe, f.ip, threshold, threshold, f.scan, f.ip)
	}
	return b.String()
}

// scanOffScript removes the detection chain and sets (blocklists stay).
const scanOffScript = `add table inet sentrel
add set inet sentrel open_tcp { type inet_service; }
add set inet sentrel scanners4 { type ipv4_addr; flags dynamic,timeout; timeout 10m; size 4096; }
add set inet sentrel scanners6 { type ipv6_addr; flags dynamic,timeout; timeout 10m; size 4096; }
add set inet sentrel probe4 { type ipv4_addr; flags dynamic,timeout; timeout 1m; size 16384; }
add set inet sentrel probe6 { type ipv6_addr; flags dynamic,timeout; timeout 1m; size 16384; }
add set inet sentrel allow4 { type ipv4_addr; flags interval; }
add set inet sentrel allow6 { type ipv6_addr; flags interval; }
add chain inet sentrel scan { type filter hook input priority -5; policy accept; }
flush chain inet sentrel scan
delete chain inet sentrel scan
delete set inet sentrel open_tcp
delete set inet sentrel scanners4
delete set inet sentrel scanners6
delete set inet sentrel probe4
delete set inet sentrel probe6
delete set inet sentrel allow4
delete set inet sentrel allow6
`

// parseScanSyncArgs validates `scan-sync PORTS [--threshold N] [--allow LIST]`.
func parseScanSyncArgs(args []string) (ports []int, threshold int, allow4, allow6 []string, err error) {
	if len(args) < 1 {
		return nil, 0, nil, nil, fmt.Errorf("usage: scan-sync PORTS [--threshold N] [--allow IP,CIDR,...]")
	}
	if ports, err = validPortList(args[0]); err != nil {
		return
	}
	threshold = scanDefaultThreshold
	for i := 1; i < len(args); i++ {
		if i+1 >= len(args) {
			return nil, 0, nil, nil, fmt.Errorf("%s needs a value", args[i])
		}
		switch args[i] {
		case "--threshold":
			if threshold, err = validScanThreshold(args[i+1]); err != nil {
				return
			}
		case "--allow":
			if allow4, allow6, err = validAddrList(args[i+1]); err != nil {
				return
			}
		default:
			return nil, 0, nil, nil, fmt.Errorf("unknown scan-sync option %q", args[i])
		}
		i++
	}
	return
}

// parseProcNetTCPListen extracts the local port from a /proc/net/tcp line in
// the LISTEN state (0A).
func parseProcNetTCPListen(line string) (int, bool) {
	f := strings.Fields(line)
	if len(f) < 4 || f[3] != "0A" {
		return 0, false
	}
	i := strings.LastIndexByte(f[1], ':')
	if i < 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(f[1][i+1:], 16, 32)
	if err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return int(n), true
}
