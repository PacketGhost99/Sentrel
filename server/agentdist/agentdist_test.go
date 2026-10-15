package agentdist

import (
	"testing"
	"testing/fstest"
)

func TestReadVersionMatchesAgentForm(t *testing.T) {
	for in, want := range map[string]string{"v0.9.6\n": "0.9.6", "0.9.6": "0.9.6", "0.10.0-rc1": "0.10.0-rc1"} {
		if got := readVersion(fstest.MapFS{"VERSION": {Data: []byte(in)}}); got != want {
			t.Errorf("readVersion(%q) = %q, want %q", in, got, want)
		}
	}
	if got := readVersion(fstest.MapFS{}); got != "unknown" {
		t.Errorf("missing VERSION = %q", got)
	}
}
