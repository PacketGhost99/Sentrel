// Package jobpolicy classifies remote jobs and defines the agent-side write
// policy, so the server and the agent agree on both.
//
// SAFE jobs only read state or undo a block; the server's signature alone is
// enough for them. Every other job type (and any type not listed here) is a
// WRITE job: it changes the host or runs code on it. What a host accepts for
// WRITE jobs is decided locally by the agent's write policy.
package jobpolicy

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// Write policies, set only in the agent's local configuration.
const (
	PolicyServer   = "server"   // the server's signature is enough (default)
	PolicyOperator = "operator" // WRITE jobs also need an operator signature
	PolicyNone     = "none"     // WRITE jobs are refused (read-only agent)
)

// ReadOnlyMessage is the result error for a WRITE job sent to a read-only agent.
const ReadOnlyMessage = "rejected: agent is read-only"

// WRITE jobs (anything not listed below) include upgrade, block_ip,
// disk_cleanup, self_update and remove_kernel.
var safe = map[string]bool{
	"refresh_inventory": true,
	"collect_now":       true,
	"disk_usage":        true,
	"unblock_ip":        true,
}

// IsSafe reports whether a job type is in the SAFE class.
func IsSafe(jobType string) bool { return safe[jobType] }

// IsWrite reports whether a job type is in the WRITE class. Unknown types are
// WRITE.
func IsWrite(jobType string) bool { return !safe[jobType] }

// NormalizePolicy returns the canonical policy name; empty means the default.
func NormalizePolicy(p string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "", PolicyServer:
		return PolicyServer, nil
	case PolicyOperator:
		return PolicyOperator, nil
	case PolicyNone, "read-only", "readonly":
		return PolicyNone, nil
	}
	return "", fmt.Errorf("write policy must be server, operator or none (got %q)", p)
}

// ParseKey decodes a base64 raw Ed25519 public key.
func ParseKey(b64 string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("operator key is not valid base64: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("operator key must be a raw 32-byte Ed25519 public key (got %d bytes)", len(b))
	}
	return ed25519.PublicKey(b), nil
}

// ParseKeys decodes a list of base64 public keys.
func ParseKeys(list []string) ([]ed25519.PublicKey, error) {
	var out []ed25519.PublicKey
	for _, s := range list {
		k, err := ParseKey(s)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

// KeyID is a short stable fingerprint of a public key (16 hex characters).
func KeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:8])
}

// KeyIDs returns the fingerprints of base64 keys, skipping invalid ones.
func KeyIDs(list []string) []string {
	out := []string{}
	for _, s := range list {
		if k, err := ParseKey(s); err == nil {
			out = append(out, KeyID(k))
		}
	}
	return out
}

// VerifyOperator checks sig over spec against the trusted keys. When keyID is
// set only the matching key is tried. It returns the key ID that verified.
func VerifyOperator(keys []ed25519.PublicKey, keyID string, spec, sig []byte) (string, bool) {
	if len(sig) != ed25519.SignatureSize {
		return "", false
	}
	for _, k := range keys {
		id := KeyID(k)
		if keyID != "" && keyID != id {
			continue
		}
		if ed25519.Verify(k, spec, sig) {
			return id, true
		}
	}
	return "", false
}
