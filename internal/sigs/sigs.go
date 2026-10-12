// Package sigs defines the exact byte strings that are signed between server
// and agent, so both sides construct them identically.
package sigs

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
)

// HelloMessage is what an agent signs to prove possession of its key when
// mTLS is not visible to the server (e.g. behind a TLS-terminating proxy).
func HelloMessage(agentID string, unixMs int64) []byte {
	return []byte("heartbeat-hello|" + agentID + "|" + strconv.FormatInt(unixMs, 10))
}

// BinaryMessage is what the server signs for an agent binary update.
func BinaryMessage(goos, goarch, version, sha256hex string) []byte {
	return []byte(fmt.Sprintf("heartbeat-binary|%s|%s|%s|%s", goos, goarch, version, sha256hex))
}

// VerifyBinary checks a binary update signature.
func VerifyBinary(pub ed25519.PublicKey, goos, goarch, version, sha256hex string, sig []byte) bool {
	return ed25519.Verify(pub, BinaryMessage(goos, goarch, version, sha256hex), sig)
}

// FileSHA256 returns the hex SHA-256 of a file.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
