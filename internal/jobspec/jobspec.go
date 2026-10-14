// Package jobspec decodes and validates the protobuf job-spec bytes that an
// operator signs, so a signer can see exactly what it is authorising.
package jobspec

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"google.golang.org/protobuf/proto"

	pb "sentrel/gen/sentrelv1"
	"sentrel/internal/jobpolicy"
)

// KnownTypes are the job types an agent understands.
var KnownTypes = map[string]bool{
	"collect_now": true, "refresh_inventory": true, "upgrade": true, "remove_kernel": true,
	"block_ip": true, "unblock_ip": true, "disk_usage": true, "disk_cleanup": true, "self_update": true,
}

// Param is one job parameter.
type Param struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Summary is every authorisation-relevant field of a job spec.
type Summary struct {
	JobID          string  `json:"job_id"`
	AgentID        string  `json:"agent_id"`
	Type           string  `json:"type"`
	WriteClass     bool    `json:"write_class"`
	Params         []Param `json:"params"`
	DryRun         bool    `json:"dry_run"`
	IssuedUnix     int64   `json:"issued_unix"`
	ExpiresUnix    int64   `json:"expires_unix"`
	TimeoutSeconds uint32  `json:"timeout_seconds"`
}

// Decode parses raw as a JobSpec and rejects anything a signer should not
// approve: malformed bytes, unknown fields, missing identity, unknown job
// types and specs that are expired (or have no expiry) at now.
func Decode(raw []byte, now time.Time) (*Summary, error) {
	if len(raw) == 0 {
		return nil, errors.New("job spec is empty")
	}
	var s pb.JobSpec
	if err := proto.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("job spec is not a valid job spec message: %w", err)
	}
	if len(s.ProtoReflect().GetUnknown()) > 0 {
		return nil, errors.New("job spec contains fields this tool does not understand; refusing to sign")
	}
	if s.Id == "" {
		return nil, errors.New("job spec has no job id")
	}
	if s.AgentId == "" {
		return nil, errors.New("job spec has no agent id")
	}
	if !KnownTypes[s.Type] {
		return nil, fmt.Errorf("unknown job type %q; refusing to sign", s.Type)
	}
	if s.ExpiresUnix <= 0 {
		return nil, errors.New("job spec has no expiry; refusing to sign")
	}
	if s.ExpiresUnix <= now.Unix() {
		return nil, fmt.Errorf("job spec expired at %s", time.Unix(s.ExpiresUnix, 0).UTC().Format(time.RFC3339))
	}
	sum := &Summary{JobID: s.Id, AgentID: s.AgentId, Type: s.Type, WriteClass: jobpolicy.IsWrite(s.Type),
		Params: []Param{}, DryRun: s.DryRun, IssuedUnix: s.IssuedUnix, ExpiresUnix: s.ExpiresUnix, TimeoutSeconds: s.TimeoutSeconds}
	for k, v := range s.Params {
		sum.Params = append(sum.Params, Param{k, v})
	}
	sort.Slice(sum.Params, func(i, j int) bool { return sum.Params[i].Key < sum.Params[j].Key })
	return sum, nil
}
