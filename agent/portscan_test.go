package agent

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidPortList(t *testing.T) {
	ok := map[string][]int{
		"-":            nil,
		"22":           {22},
		"443,22,80,22": {22, 80, 443},
		" 1,65535 ":    {1, 65535},
	}
	for in, want := range ok {
		got, err := validPortList(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v %v, want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "65536", "-1", "22,", ",22", "22,,80", "2 2", "22;reboot", "22 }", "0x16", "1e3", "99999", "a", "22,80\nflush ruleset"} {
		if got, err := validPortList(in); err == nil {
			t.Errorf("%q accepted: %v", in, got)
		}
	}
	if s := formatPortList([]int{80, 22, 80}); s != "22,80" {
		t.Errorf("format: %q", s)
	}
	if s := formatPortList(nil); s != "-" {
		t.Errorf("format empty: %q", s)
	}
}

func TestValidAddrList(t *testing.T) {
	v4, v6, err := validAddrList("10.0.0.0/8,192.0.2.1,2001:db8::/32,2001:db8::1,192.0.2.1")
	if err != nil || !reflect.DeepEqual(v4, []string{"10.0.0.0/8", "192.0.2.1"}) || !reflect.DeepEqual(v6, []string{"2001:db8::/32", "2001:db8::1"}) {
		t.Fatalf("%v %v %v", v4, v6, err)
	}
	for _, in := range []string{"", "x", "1.2.3.4/33", "1.2.3.4 }; flush ruleset", "1.2.3.4,,5.6.7.8"} {
		if _, _, err := validAddrList(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestValidScanThreshold(t *testing.T) {
	for in, ok := range map[string]bool{"20": true, "1": true, "100000": true, "0": false, "-5": false, "100001": false, "x": false, "": false} {
		if _, err := validScanThreshold(in); (err == nil) != ok {
			t.Errorf("%q: err=%v", in, err)
		}
	}
}

func TestParseScanSyncArgs(t *testing.T) {
	ports, th, a4, a6, err := parseScanSyncArgs([]string{"22,80", "--threshold", "30", "--allow", "10.0.0.0/8,2001:db8::1"})
	if err != nil || !reflect.DeepEqual(ports, []int{22, 80}) || th != 30 || len(a4) != 1 || len(a6) != 1 {
		t.Fatalf("%v %v %v %v %v", ports, th, a4, a6, err)
	}
	_, th, _, _, err = parseScanSyncArgs([]string{"-"})
	if err != nil || th != scanDefaultThreshold {
		t.Fatalf("defaults: %v %v", th, err)
	}
	for _, args := range [][]string{nil, {"22", "--threshold"}, {"22", "--bogus", "1"}, {"22", "--threshold", "0"}, {"x"}, {"22", "--allow", "nope"}} {
		if _, _, _, _, err := parseScanSyncArgs(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestBuildScanScript(t *testing.T) {
	s := buildScanScript([]int{22, 443}, 30, []string{"10.0.0.0/8"}, nil)
	for _, want := range []string{
		"flush chain inet sentrel scan",
		"add element inet sentrel open_tcp { 22, 443 }",
		"add element inet sentrel allow4 { 10.0.0.0/8 }",
		"iif lo return",
		"limit rate over 30/minute burst 30 packets",
		"update @scanners6",
		"policy accept",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(s, "add element inet sentrel allow6") {
		t.Error("empty allow6 element emitted")
	}
	// Detection only: nothing in the script may drop, reject or modify traffic.
	for _, bad := range []string{" drop", "reject", "dnat", "snat", "masquerade", "queue", "mangle"} {
		if strings.Contains(s, bad) {
			t.Errorf("script contains %q", bad)
		}
	}
	// An empty open-port list emits no element line (nft rejects an empty one).
	if strings.Contains(buildScanScript(nil, 20, nil, nil), "add element inet sentrel open_tcp") {
		t.Error("empty element list emitted")
	}
	// The blocklist sets and their chain are never flushed or deleted.
	if strings.Contains(s+scanOffScript, "blocklist") {
		t.Error("scan scripts touch the blocklist")
	}
}

const nftSetJSON = `{"nftables":[{"metainfo":{"version":"1.1.6"}},{"set":{"family":"inet","name":"scanners4","table":"sentrel","type":"ipv4_addr","handle":2,"flags":["timeout","dynamic"],"timeout":600,"elem":[{"elem":{"val":"203.0.113.5","expires":599}},{"elem":{"val":"198.51.100.9","expires":120}},"192.0.2.1",{"elem":{"val":"junk","expires":5}}]}}]}`

func TestParseScanSet(t *testing.T) {
	got, err := parseScanSet([]byte(nftSetJSON))
	if err != nil {
		t.Fatal(err)
	}
	want := []ScanEntry{{"203.0.113.5", 599}, {"198.51.100.9", 120}, {"192.0.2.1", scanTimeoutSec}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	// An empty set has no elem key.
	got, err = parseScanSet([]byte(`{"nftables":[{"set":{"name":"scanners4"}}]}`))
	if err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	if _, err := parseScanSet([]byte("not json")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestScanTrackerDedupAndRate(t *testing.T) {
	tr := newScanTracker()
	t0 := time.Unix(1_700_000_000, 0)

	// First sighting reports once, with last-seen derived from the timeout.
	r := tr.Observe(t0, []ScanEntry{{"203.0.113.5", 590}})
	if len(r) != 1 || r[0].Ip != "203.0.113.5" || r[0].LastSeenMs != t0.Add(-10*time.Second).UnixMilli() {
		t.Fatalf("first: %v", r)
	}
	// Polling again without new probes reports nothing.
	for i := 1; i <= 5; i++ {
		if r := tr.Observe(t0.Add(time.Duration(i)*30*time.Second), []ScanEntry{{"203.0.113.5", int64(590 - i*30)}}); len(r) != 0 {
			t.Fatalf("poll %d re-reported: %v", i, r)
		}
	}
	// Still scanning (timeout refreshed), but less than 5 minutes since the report.
	if r := tr.Observe(t0.Add(3*time.Minute), []ScanEntry{{"203.0.113.5", 600}}); len(r) != 0 {
		t.Fatalf("early re-report: %v", r)
	}
	// Five minutes of continued activity: reported again, keeping first_seen.
	r = tr.Observe(t0.Add(6*time.Minute), []ScanEntry{{"203.0.113.5", 600}})
	if len(r) != 1 || r[0].FirstSeenMs != t0.Add(-10*time.Second).UnixMilli() || r[0].LastSeenMs != t0.Add(6*time.Minute).UnixMilli() {
		t.Fatalf("re-report: %v", r)
	}
	// The entry expires, then the source returns: a fresh report.
	tr.Observe(t0.Add(20*time.Minute), nil)
	r = tr.Observe(t0.Add(25*time.Minute), []ScanEntry{{"203.0.113.5", 600}})
	if len(r) != 1 || r[0].FirstSeenMs != t0.Add(25*time.Minute).UnixMilli() {
		t.Fatalf("return: %v", r)
	}
	// IPv6 is normalised and bad input ignored.
	r = tr.Observe(t0.Add(26*time.Minute), []ScanEntry{{"2001:0db8:0:0:0:0:0:1", 600}, {"junk", 5}})
	if len(r) != 1 || r[0].Ip != "2001:db8::1" {
		t.Fatalf("v6: %v", r)
	}
}

func TestScanTrackerBounds(t *testing.T) {
	tr := newScanTracker()
	now := time.Now()
	var es []ScanEntry
	for i := 0; i < 250; i++ {
		es = append(es, ScanEntry{IP: "10.1.0." + itoa(int64(i+1)), Remaining: 600})
	}
	if r := tr.Observe(now, es); len(r) != scanMaxPerReport {
		t.Fatalf("report size %d", len(r))
	}
	// The rest follow on later polls instead of being lost.
	if r := tr.Observe(now.Add(30*time.Second), es); len(r) != scanMaxPerReport {
		t.Fatalf("second batch %d", len(r))
	}
	if r := tr.Observe(now.Add(60*time.Second), es); len(r) != 50 {
		t.Fatalf("last batch %d", len(r))
	}
	if r := tr.Observe(now.Add(90*time.Second), es); len(r) != 0 {
		t.Fatalf("done, got %d", len(r))
	}
}

func TestScanSyncKeyStable(t *testing.T) {
	a := scanSyncKey([]int{80, 22}, 20, []string{"b", "a"}, []string{"y", "x"})
	b := scanSyncKey([]int{22, 80}, 20, []string{"a", "b"}, []string{"x", "y"})
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
	if a == scanSyncKey([]int{22, 80}, 21, []string{"a", "b"}, []string{"x", "y"}) {
		t.Fatal("threshold ignored")
	}
}

func TestServerHostAndAllowEntries(t *testing.T) {
	for in, want := range map[string]string{"https://sentrel.example.com:8443": "sentrel.example.com", "10.1.2.3:9000": "10.1.2.3", "": ""} {
		if got := serverHost(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
	if got := validAllowEntries([]string{"10.0.0.0/8", " ", "bad", "192.0.2.1"}); !reflect.DeepEqual(got, []string{"10.0.0.0/8", "192.0.2.1"}) {
		t.Errorf("allow entries: %v", got)
	}
}
func TestParseProcNetTCPListen(t *testing.T) {
	lines := map[string]int{
		"   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0":                    22,
		"   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000   120        0 23456 1 0000000000000000 100 0 0 10 0":                    8080,
		"   2: 0A00A8C0:D2F4 5DB8D822:01BB 01 00000000:00000000 02:000A2BC7 00000000  1000        0 34567 2 0000000000000000 20 4 30 10 -1":                  0,
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode":                                                      0,
		"   3: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 4 1 0": 443,
	}
	for l, want := range lines {
		got, ok := parseProcNetTCPListen(l)
		if want == 0 && ok || want != 0 && (!ok || got != want) {
			t.Errorf("%q: %d %v want %d", l, got, ok, want)
		}
	}
}
