package jobspec

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "sentrel/gen/sentrelv1"
)

var now = time.Unix(1_800_000_000, 0)

func enc(t *testing.T, s *pb.JobSpec) []byte {
	t.Helper()
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func good() *pb.JobSpec {
	return &pb.JobSpec{Id: "j1", AgentId: "a1", Type: "block_ip", Params: map[string]string{"ip": "203.0.113.9", "duration": "3600"},
		IssuedUnix: now.Unix() - 5, ExpiresUnix: now.Unix() + 600, DryRun: true, TimeoutSeconds: 90}
}

func TestRoundTrip(t *testing.T) {
	s, err := Decode(enc(t, good()), now)
	if err != nil {
		t.Fatal(err)
	}
	if s.JobID != "j1" || s.AgentID != "a1" || s.Type != "block_ip" || !s.DryRun || !s.WriteClass || s.TimeoutSeconds != 90 ||
		s.IssuedUnix != now.Unix()-5 || s.ExpiresUnix != now.Unix()+600 {
		t.Fatalf("bad summary %+v", s)
	}
	if len(s.Params) != 2 || s.Params[0].Key != "duration" || s.Params[1].Value != "203.0.113.9" {
		t.Fatalf("params %+v", s.Params)
	}
}

func TestRejects(t *testing.T) {
	unknownType := good()
	unknownType.Type = "script"
	expired := good()
	expired.ExpiresUnix = now.Unix() - 1
	noExpiry := good()
	noExpiry.ExpiresUnix = 0
	noID := good()
	noID.Id = ""
	noAgent := good()
	noAgent.AgentId = ""
	withUnknown := append(enc(t, good()), 0x98, 0x06, 0x01) // field 99 varint
	cases := map[string][]byte{
		"empty": nil, "garbage": []byte("exact job spec bytes"), "truncated": enc(t, good())[:7],
		"unknown type": enc(t, unknownType), "expired": enc(t, expired), "no expiry": enc(t, noExpiry),
		"no id": enc(t, noID), "no agent": enc(t, noAgent), "unknown field": withUnknown,
	}
	for name, raw := range cases {
		if _, err := Decode(raw, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
