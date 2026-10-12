package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "sentrel/gen/sentrelv1"
)

func TestFailureCounterSSH(t *testing.T) {
	fc := newFailureCounter()
	lines := []string{
		// One connection: invalid user, then the password failure, then preauth close => 1 failure.
		"Oct 12 07:00:01 host sshd[1]: Invalid user admin from 203.0.113.5 port 40000",
		"Oct 12 07:00:02 host sshd[1]: Failed password for invalid user admin from 203.0.113.5 port 40000 ssh2",
		"Oct 12 07:00:03 host sshd[1]: Connection closed by invalid user admin 203.0.113.5 port 40000 [preauth]",
		// Second connection, three password attempts => 3 failures.
		"sshd[2]: Failed password for root from 203.0.113.5 port 40001 ssh2",
		"sshd[2]: Failed password for root from 203.0.113.5 port 40001 ssh2",
		"sshd[2]: Failed password for root from 203.0.113.5 port 40001 ssh2",
		// Scanner that never tries a password: counts once.
		"sshd-session[3]: Connection closed by authenticating user ubuntu 198.51.100.7 port 5555 [preauth]",
		// IPv6 and publickey.
		"sshd[4]: Failed publickey for git from 2001:db8::1 port 1234 ssh2",
		// Noise.
		"sshd[5]: Accepted publickey for ubuntu from 192.0.2.10 port 22 ssh2",
		"CRON[6]: pam_unix(cron:session): session opened for user root",
	}
	for _, l := range lines {
		fc.ParseAuthLine(l)
	}
	fc.Add("192.0.2.200", "Administrator", 2) // Windows-style
	_, _, out := fc.Flush()
	got := map[string]uint32{}
	for _, f := range out {
		got[f.Ip] = f.Count
	}
	want := map[string]uint32{"203.0.113.5": 4, "198.51.100.7": 1, "2001:db8::1": 1, "192.0.2.200": 2}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for ip, n := range want {
		if got[ip] != n {
			t.Errorf("%s: got %d want %d", ip, got[ip], n)
		}
	}
	if _, _, again := fc.Flush(); len(again) != 0 {
		t.Error("flush did not reset the window")
	}
}

func TestLinearSlope(t *testing.T) {
	var h []diskPoint
	for i := 0; i < 24; i++ {
		h = append(h, diskPoint{T: int64(i * 300), Used: float64(1000 + i*300*10)}) // 10 bytes/s
	}
	if s := linearSlope(h); s < 9.99 || s > 10.01 {
		t.Fatalf("slope %v", s)
	}
}

func TestRecordDiskNeedsAnHour(t *testing.T) {
	c := NewCollector("")
	now := time.Now()
	for i := 0; i < 11; i++ {
		if _, ok := c.recordDisk("/", now.Add(time.Duration(i)*5*time.Minute), float64(i)); ok {
			t.Fatal("prediction with under an hour of data")
		}
	}
	if _, ok := c.recordDisk("/", now.Add(65*time.Minute), 100); !ok {
		t.Fatal("no prediction after an hour")
	}
}

func TestParseUpgradeArgs(t *testing.T) {
	o, err := parseUpgradeArgs([]string{"--dry-run", "--security-only", "--packages", "openssl,libssl3:amd64"})
	if err != nil || !o.DryRun || !o.SecurityOnly || len(o.Packages) != 2 {
		t.Fatalf("%+v %v", o, err)
	}
	for _, bad := range [][]string{{"--packages", "openssl;id"}, {"--packages", "-oAPT::x"}, {"--bogus"}, {"--packages"}} {
		if _, err := parseUpgradeArgs(bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestValidIP(t *testing.T) {
	for _, ok := range []string{"203.0.113.9", "2001:db8::5"} {
		if _, err := validIP(ok); err != nil {
			t.Errorf("%s rejected", ok)
		}
	}
	for _, bad := range []string{"127.0.0.1", "::1", "0.0.0.0", "1.2.3.4; rm -rf /", "", "10.0.0.0/8"} {
		if _, err := validIP(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := validMinutes("0"); err == nil {
		t.Error("0 minutes accepted")
	}
}

func TestVerifyJob(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := &Agent{cfg: &Config{AgentID: "agent-1", StateDir: t.TempDir()}, log: slog.New(slog.NewTextHandler(io.Discard, nil)), jobKey: pub, seenJobs: map[string]time.Time{}}
	sign := func(s *pb.JobSpec) *pb.SignedJob {
		b, _ := proto.Marshal(s)
		return &pb.SignedJob{Spec: b, Signature: ed25519.Sign(priv, b)}
	}
	now := time.Now().Unix()
	good := &pb.JobSpec{Id: "j1", AgentId: "agent-1", Type: "collect_now", IssuedUnix: now, ExpiresUnix: now + 60}
	if _, err := a.verifyJob(sign(good)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.verifyJob(sign(good)); err == nil {
		t.Fatal("replay accepted")
	}
	for name, s := range map[string]*pb.JobSpec{
		"other agent": {Id: "j2", AgentId: "agent-2", ExpiresUnix: now + 60},
		"expired":     {Id: "j3", AgentId: "agent-1", ExpiresUnix: now - 1},
		"future":      {Id: "j4", AgentId: "agent-1", IssuedUnix: now + 3600, ExpiresUnix: now + 7200},
	} {
		if _, err := a.verifyJob(sign(s)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Wrong key.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	b, _ := proto.Marshal(&pb.JobSpec{Id: "j5", AgentId: "agent-1", ExpiresUnix: now + 60})
	if _, err := a.verifyJob(&pb.SignedJob{Spec: b, Signature: ed25519.Sign(other, b)}); err == nil {
		t.Error("foreign signature accepted")
	}
}

func TestServerTarget(t *testing.T) {
	for in, want := range map[string]string{"https://mon.example.com": "mon.example.com:443", "http://10.0.0.1:8080/": "10.0.0.1:8080", "https://[2001:db8::1]:9443": "[2001:db8::1]:9443"} {
		got, _, _, err := serverTarget(in)
		if err != nil || got != want {
			t.Errorf("%s: %s %v", in, got, err)
		}
	}
	if _, _, _, err := serverTarget("ftp://x"); err == nil {
		t.Error("ftp accepted")
	}
}

func TestSeenJobsPersist(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	newAgent := func() *Agent {
		return &Agent{cfg: &Config{AgentID: "agent-1", StateDir: dir}, log: slog.New(slog.NewTextHandler(io.Discard, nil)), jobKey: pub, seenJobs: map[string]time.Time{}}
	}
	now := time.Now().Unix()
	b, _ := proto.Marshal(&pb.JobSpec{Id: "persist-1", AgentId: "agent-1", IssuedUnix: now, ExpiresUnix: now + 60})
	sj := &pb.SignedJob{Spec: b, Signature: ed25519.Sign(priv, b)}
	a := newAgent()
	if _, err := a.verifyJob(sj); err != nil {
		t.Fatal(err)
	}
	// "Restart": a new agent loads the file and must reject the replay.
	a2 := newAgent()
	a2.loadSeenJobs()
	if _, err := a2.verifyJob(sj); err == nil {
		t.Fatal("replay after restart accepted")
	}
	// Expired entries are dropped on load.
	a3 := newAgent()
	a3.seenJobs["old"] = time.Now().Add(-time.Minute)
	a3.saveSeenJobs()
	a4 := newAgent()
	a4.loadSeenJobs()
	if _, ok := a4.seenJobs["old"]; ok {
		t.Error("expired entry kept")
	}
	if _, ok := a4.seenJobs["persist-1"]; ok {
		t.Error("a3 save should not contain persist-1 (it never loaded it)")
	}
}
