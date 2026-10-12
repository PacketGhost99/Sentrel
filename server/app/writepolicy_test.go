package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "sentrel/gen/sentrelv1"
	"sentrel/internal/jobpolicy"
)

// setPolicy simulates the agent reporting its local write policy in its hello.
func (e *testEnv) setPolicy(t *testing.T, hostID int64, policy string, pubs ...ed25519.PublicKey) {
	t.Helper()
	hello := &pb.Hello{WritePolicy: policy}
	for _, p := range pubs {
		hello.OperatorKeys = append(hello.OperatorKeys, base64.StdEncoding.EncodeToString(p))
	}
	pol, keys, ids := reportedWritePolicy(hello)
	kb, _ := json.Marshal(keys)
	ib, _ := json.Marshal(ids)
	if _, err := e.a.db.Exec(`UPDATE hosts SET write_policy=?, operator_keys=?, operator_key_ids=? WHERE id=?`, pol, string(kb), string(ib), hostID); err != nil {
		t.Fatal(err)
	}
}

func (e *testEnv) fakeConn(hostID int64) *agentConn {
	conn := &agentConn{hostID: hostID, send: make(chan *pb.ServerMessage, 8), cancel: func() {}}
	e.a.hub.mu.Lock()
	e.a.hub.conns[hostID] = conn
	e.a.hub.mu.Unlock()
	return conn
}

func TestOperatorSignedJobFlow(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, resp := e.enrol(admin)
	ctx := context.Background()
	opPub, opPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	e.setPolicy(t, hostID, "operator", opPub)
	conn := e.fakeConn(hostID)

	// Host JSON shows the policy and key ids.
	_, h, _ := admin.do("GET", "/hosts/"+itoa(hostID), nil)
	ids, _ := h["operator_key_ids"].([]any)
	if h["write_policy"] != "operator" || len(ids) != 1 || ids[0] != jobpolicy.KeyID(opPub) {
		t.Fatalf("host json: %v %v", h["write_policy"], h["operator_key_ids"])
	}

	// SAFE jobs are queued as usual.
	if code, m, raw := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "collect_now"}); code != 201 || m["status"] != "queued" {
		t.Fatalf("safe job: %d %s", code, raw)
	}
	// WRITE jobs wait for a signature.
	code, m, raw := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "upgrade", "dry_run": true})
	if code != 201 || m["status"] != "awaiting_signature" {
		t.Fatalf("write job: %d %s", code, raw)
	}
	id := m["id"].(string)
	e.a.jobs.tick(ctx)
	got := 0
drain:
	for {
		select {
		case msg := <-conn.send:
			got++
			if sj := msg.GetJob(); sj != nil {
				var s pb.JobSpec
				_ = proto.Unmarshal(sj.Spec, &s)
				if s.Id == id {
					t.Fatal("awaiting job was dispatched")
				}
			}
		default:
			break drain
		}
	}
	if got != 1 {
		t.Fatalf("expected only the safe job to be sent, got %d", got)
	}

	// Offline signing: fetch the exact bytes.
	code, sp, raw := admin.do("GET", "/jobs/"+id+"/spec", nil)
	if code != 200 {
		t.Fatalf("spec: %d %s", code, raw)
	}
	spec, err := base64.StdEncoding.DecodeString(sp["spec"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var parsed pb.JobSpec
	if err := proto.Unmarshal(spec, &parsed); err != nil || parsed.Id != id || parsed.Type != "upgrade" || parsed.AgentId != resp.AgentId {
		t.Fatalf("spec content: %v %+v", err, &parsed)
	}
	// Wrong key / wrong bytes are refused; the job stays put.
	bad := base64.StdEncoding.EncodeToString(ed25519.Sign(otherPriv, spec))
	if code, _, _ := admin.do("POST", "/jobs/"+id+"/operator-signature", map[string]any{"signature": bad}); code != 400 {
		t.Fatalf("foreign signature: %d", code)
	}
	bad = base64.StdEncoding.EncodeToString(ed25519.Sign(opPriv, append([]byte("x"), spec...)))
	if code, _, _ := admin.do("POST", "/jobs/"+id+"/operator-signature", map[string]any{"signature": bad}); code != 400 {
		t.Fatalf("signature over other bytes: %d", code)
	}
	if j, _ := e.a.getJob(ctx, id); j.Status != "awaiting_signature" {
		t.Fatalf("status %s", j.Status)
	}
	// Valid signature queues it.
	good := base64.StdEncoding.EncodeToString(ed25519.Sign(opPriv, spec))
	code, m, raw = admin.do("POST", "/jobs/"+id+"/operator-signature", map[string]any{"signature": good, "key_id": jobpolicy.KeyID(opPub)})
	if code != 200 || m["status"] != "queued" {
		t.Fatalf("sign: %d %s", code, raw)
	}
	if code, _, _ := admin.do("POST", "/jobs/"+id+"/operator-signature", map[string]any{"signature": good}); code != 409 {
		t.Fatalf("second signature: %d", code)
	}
	e.a.jobs.tick(ctx)
	select {
	case msg := <-conn.send:
		sj := msg.GetJob()
		if sj == nil {
			t.Fatal("no job")
		}
		if string(sj.Spec) != string(spec) {
			t.Fatal("dispatched spec differs from the bytes the operator signed")
		}
		if !ed25519.Verify(ed25519.PublicKey(resp.JobPublicKey), sj.Spec, sj.Signature) {
			t.Fatal("server signature missing")
		}
		if _, ok := jobpolicy.VerifyOperator([]ed25519.PublicKey{opPub}, sj.OperatorKeyId, sj.Spec, sj.OperatorSignature); !ok {
			t.Fatal("operator signature missing")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("signed job not dispatched")
	}
}

func TestReadOnlyHostRefusesWriteJobs(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	e.setPolicy(t, hostID, "none")
	for _, typ := range []string{"upgrade", "self_update", "script", "block_ip", "disk_cleanup"} {
		code, m, raw := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": typ, "dry_run": true, "confirm": true,
			"params": map[string]string{"ip": "192.0.2.1", "script": "id", "tasks": "tmp"}})
		if code != 403 || !strings.Contains(m["error"].(string), "read-only") {
			t.Errorf("%s: %d %s", typ, code, raw)
		}
	}
	if code, _, raw := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "refresh_inventory"}); code != 201 {
		t.Fatalf("safe job on read-only host: %d %s", code, raw)
	}
	// Bulk: each host is handled per its policy.
	h2, _ := e.enrol(admin)
	code, _, raw := admin.do("POST", "/jobs/bulk", map[string]any{"host_ids": []int64{hostID, h2}, "type": "upgrade", "dry_run": true})
	if code != 200 && code != 201 {
		t.Fatalf("bulk: %d %s", code, raw)
	}
	var out []map[string]any
	_ = json.Unmarshal(raw, &out)
	if len(out) != 2 || out[0]["error"] == nil || out[1]["job_id"] == nil {
		t.Fatalf("bulk result: %s", raw)
	}
}

func TestAwaitingJobsExpireAndAutoUpdateSkips(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	ctx := context.Background()
	opHost, _ := e.enrol(admin)
	roHost, _ := e.enrol(admin)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	e.setPolicy(t, opHost, "operator", pub)
	e.setPolicy(t, roHost, "none")

	_, m, _ := admin.do("POST", "/jobs", map[string]any{"host_id": opHost, "type": "upgrade", "dry_run": true})
	id := m["id"].(string)
	if _, err := e.a.db.Exec(`UPDATE jobs SET expires_at=? WHERE id=?`, time.Now().Unix()-10, id); err != nil {
		t.Fatal(err)
	}
	e.a.jobs.tick(ctx)
	if j, _ := e.a.getJob(ctx, id); j.Status != "expired" {
		t.Fatalf("awaiting job status %s", j.Status)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sentrel-agent-linux-amd64"), []byte("fake-binary"), 0o755)
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("2.0.0\n"), 0o644)
	e.a.cfg.AgentBinDir = dir
	d := e.a.settings.Agent()
	d.AutoUpdate = true
	if err := e.a.settings.Save(ctx, 0, keyAgent, d); err != nil {
		t.Fatal(err)
	}
	for _, hid := range []int64{opHost, roHost} {
		e.a.db.Exec(`UPDATE hosts SET agent_version='1.0.0' WHERE id=?`, hid)
		h, _ := e.a.getHost(ctx, hid)
		e.a.jobs.maybeAutoUpdate(ctx, h)
	}
	var n int
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM jobs WHERE type='self_update'`)
	if n != 0 {
		t.Fatalf("auto-update created %d jobs for restricted hosts", n)
	}
	// An explicit self_update on an operator host waits for a signature and
	// has its download parameters fixed in the spec.
	code, m, raw := admin.do("POST", "/jobs", map[string]any{"host_id": opHost, "type": "self_update", "confirm": true})
	if code != 201 || m["status"] != "awaiting_signature" {
		t.Fatalf("self_update: %d %s", code, raw)
	}
	_, sp, _ := admin.do("GET", "/jobs/"+m["id"].(string)+"/spec", nil)
	spec, _ := base64.StdEncoding.DecodeString(sp["spec"].(string))
	var s pb.JobSpec
	_ = proto.Unmarshal(spec, &s)
	if s.Params["sha256"] == "" || s.Params["signature"] == "" || s.Params["version"] != "2.0.0" {
		t.Fatalf("self_update params not fixed in spec: %v", s.Params)
	}
}

// The server cannot change what an agent enforces: host updates and agent
// options have no way to set the policy, and the pushed agent config has no
// policy fields.
func TestServerCannotChangeWritePolicy(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	e.setPolicy(t, hostID, "operator", pub)
	admin.do("PUT", "/hosts/"+itoa(hostID), map[string]any{"write_policy": "server", "operator_keys": []string{"AAAA"},
		"agent_options": map[string]any{"write_jobs": "server", "operator_keys": []string{"AAAA"}, "top_processes": 3}})
	h, err := e.a.getHost(context.Background(), hostID)
	if err != nil {
		t.Fatal(err)
	}
	if h.WritePolicy != "operator" || len(h.OperatorKeys) != 1 {
		t.Fatalf("host update changed the policy: %s %v", h.WritePolicy, h.OperatorKeys)
	}
	cfg := e.a.agentConfig(h)
	fields := cfg.ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		n := string(fields.Get(i).Name())
		if strings.Contains(n, "write") || strings.Contains(n, "operator") || strings.Contains(n, "policy") {
			t.Errorf("pushed AgentConfig carries %q", n)
		}
	}
}
