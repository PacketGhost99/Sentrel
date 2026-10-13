//go:build testkey

package release

import "crypto/ed25519"

// testKeyB64 is injected with -ldflags "-X sentrel/internal/release.testKeyB64=..."
// into binaries built with -tags testkey. Such binaries trust ONLY that key, so
// updater tests never touch the production trust root. Never used in releases.
var testKeyB64 string

// TrustRoot returns the throwaway test key.
func TrustRoot() ed25519.PublicKey {
	k, err := ParsePublicKey(testKeyB64)
	if err != nil {
		panic("release: testkey build without a valid -X testKeyB64: " + err.Error())
	}
	return k
}

// TestKeyBuild reports whether this binary trusts a throwaway test key.
const TestKeyBuild = true
