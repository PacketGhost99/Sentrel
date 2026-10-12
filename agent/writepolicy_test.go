package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "sentrel/gen/sentrelv1"
	"sentrel/internal/jobpolicy"
)

type policyEnv struct {
	jobPub  ed25519.PublicKey
	jobPriv ed25519.PrivateKey
	opPub   ed25519.PublicKey
	opPriv  ed25519.PrivateKey
}

func newPolicyEnv() *policyEnv {
	e := &policyEnv{}
	e.jobPub, e.jobPriv, _ = ed25519.GenerateKey(rand.Reader)
	e.opPub, e.opPriv, _ = ed25519.GenerateKey(rand.Reader)
	return e
}

func (e *policyEnv) agent(t *testing.T, policy string, withOpKey bool) *Agent {
	cfg := &Config{AgentID: "agent-1", StateDir: t.TempDir(), WriteJobs: policy}
	a := &Agent{cfg: cfg, log: slog.New(slog.NewTextHandler(io.Discard, nil)), jobKey: e.jobPub, seenJobs: map[string]time.Time{}, writePolicy: policy}
	if withOpKey {
		a.opKeys = []ed25519.PublicKey{e.opPub}
		cfg.OperatorKeys = []string{base64.StdEncoding.EncodeToString(e.opPub)}
	}
	return a
}

var jobSeq int

func (e *policyEnv) job(typ string, opSig func([]byte) ([]byte, string)) *pb.SignedJob {
	jobSeq++
	now := time.Now().Unix()
	b, _ := proto.Marshal(&pb.JobSpec{Id: fmt.Sprintf("j%d", jobSeq), AgentId: "agent-1", Type: typ, IssuedUnix: now, ExpiresUnix: now + 60})
	sj := &pb.SignedJob{Spec: b, Signature: ed25519.Sign(e.jobPriv, b)}
	if opSig != nil {
		sj.OperatorSignature, sj.OperatorKeyId = opSig(b)
	}
	return sj
}

var writeTypes = []string{"upgrade", "script", "block_ip", "disk_cleanup", "self_update", "something_new"}
var safeTypes = []string{"refresh_inventory", "collect_now", "disk_usage", "unblock_ip"}

func TestReadOnlyAgent(t *testing.T) {
	e := newPolicyEnv()
	a := e.agent(t, jobpolicy.PolicyNone, false)
	for _, typ := range writeTypes {
		_, err := a.verifyJob(e.job(typ, nil))
		if err == nil || err.Error() != "rejected: agent is read-only" {
			t.Errorf("%s: got %v", typ, err)
		}
	}
	for _, typ := range safeTypes {
		if _, err := a.verifyJob(e.job(typ, nil)); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
}

func TestOperatorAgent(t *testing.T) {
	e := newPolicyEnv()
	a := e.agent(t, jobpolicy.PolicyOperator, true)
	good := func(b []byte) ([]byte, string) { return ed25519.Sign(e.opPriv, b), jobpolicy.KeyID(e.opPub) }
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	wrong := func(b []byte) ([]byte, string) { return ed25519.Sign(stranger, b), "" }
	// The server's own key must not double as an operator key.
	serverAsOp := func(b []byte) ([]byte, string) { return ed25519.Sign(e.jobPriv, b), "" }
	wrongSpec := func(b []byte) ([]byte, string) { return ed25519.Sign(e.opPriv, append([]byte("x"), b...)), "" }
	for _, typ := range writeTypes {
		if _, err := a.verifyJob(e.job(typ, nil)); err == nil {
			t.Errorf("%s: accepted without operator signature", typ)
		}
		for name, f := range map[string]func([]byte) ([]byte, string){"wrong key": wrong, "server key": serverAsOp, "wrong spec": wrongSpec} {
			if _, err := a.verifyJob(e.job(typ, f)); err == nil {
				t.Errorf("%s: accepted with %s", typ, name)
			}
		}
		if _, err := a.verifyJob(e.job(typ, good)); err != nil {
			t.Errorf("%s: valid operator signature rejected: %v", typ, err)
		}
	}
	// A valid operator signature does not replace the server signature.
	sj := e.job("script", good)
	sj.Signature = ed25519.Sign(stranger, sj.Spec)
	if _, err := a.verifyJob(sj); err == nil {
		t.Error("operator signature accepted without the server signature")
	}
	for _, typ := range safeTypes {
		if _, err := a.verifyJob(e.job(typ, nil)); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
	// No keys configured: every write job is refused.
	b := e.agent(t, jobpolicy.PolicyOperator, false)
	if _, err := b.verifyJob(e.job("script", good)); err == nil {
		t.Error("operator agent without keys accepted a write job")
	}
}

func TestServerPolicyDefault(t *testing.T) {
	e := newPolicyEnv()
	a := e.agent(t, jobpolicy.PolicyServer, false)
	for _, typ := range append(append([]string{}, writeTypes...), safeTypes...) {
		if _, err := a.verifyJob(e.job(typ, nil)); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
}

// Server-pushed configuration cannot touch the write policy: AgentConfig has
// no such fields, and applying it leaves the local policy and keys alone.
func TestPushedConfigCannotChangePolicy(t *testing.T) {
	e := newPolicyEnv()
	a := e.agent(t, jobpolicy.PolicyNone, true)
	a.conf = defaultConfig()
	a.applyConfig(&pb.AgentConfig{IntervalSeconds: 10, TopProcesses: 5, CollectServices: true})
	if a.writePolicy != jobpolicy.PolicyNone || len(a.opKeys) != 1 || a.cfg.WriteJobs != jobpolicy.PolicyNone || len(a.cfg.OperatorKeys) != 1 {
		t.Fatal("pushed config changed the write policy")
	}
	if _, err := a.verifyJob(e.job("script", nil)); err == nil {
		t.Fatal("write job accepted after config push")
	}
	// Structural guard: the pushed config message has no policy-related field.
	fields := (&pb.AgentConfig{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		n := string(fields.Get(i).Name())
		if strings.Contains(n, "write") || strings.Contains(n, "operator") || strings.Contains(n, "policy") {
			t.Errorf("AgentConfig has a policy field %q", n)
		}
	}
}

func TestConfigPolicyLoad(t *testing.T) {
	dir := t.TempDir()
	write := func(extra string) string {
		p := filepath.Join(dir, "agent.yaml")
		os.WriteFile(p, []byte("server_url: https://x\nagent_id: a\n"+extra), 0o600)
		return p
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	k := base64.StdEncoding.EncodeToString(pub)
	c, err := LoadConfig(write("write_jobs: operator\noperator_keys: [" + k + "]\n"))
	if err != nil || c.WriteJobs != "operator" || len(c.OperatorKeys) != 1 {
		t.Fatal(c, err)
	}
	if _, err := LoadConfig(write("write_jobs: bogus\n")); err == nil {
		t.Error("bad policy accepted")
	}
	if _, err := LoadConfig(write("operator_keys: [AAAA]\n")); err == nil {
		t.Error("bad key accepted")
	}
}
