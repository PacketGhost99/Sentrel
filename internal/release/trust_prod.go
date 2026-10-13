//go:build !testkey

package release

import "crypto/ed25519"

// TrustRoot returns the key that release signatures must verify against.
func TrustRoot() ed25519.PublicKey {
	k, err := ParsePublicKey(publicKeyB64)
	if err != nil {
		panic("release: embedded public key is invalid: " + err.Error())
	}
	return k
}

// TestKeyBuild reports whether this binary trusts a throwaway test key.
const TestKeyBuild = false
