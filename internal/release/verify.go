package release

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ParsePublicKey decodes a base64 PKIX Ed25519 public key.
func ParsePublicKey(b64 string) (ed25519.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("public key is not Ed25519")
	}
	return pub, nil
}

// Sign returns the base64 signature of data, in the format of SHA256SUMS.sig.
func Sign(priv ed25519.PrivateKey, data []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
}

// VerifySignature checks a base64 signature file (SHA256SUMS.sig) over data.
func VerifySignature(pub ed25519.PublicKey, data, sigFile []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("no trusted public key")
	}
	if len(strings.TrimSpace(string(sigFile))) == 0 {
		return errors.New("signature file is empty")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigFile)))
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(pub, data, sig) {
		return errors.New("signature verification FAILED")
	}
	return nil
}

// ParseSums parses sha256sum output ("<hex>  <name>") into name -> hex.
func ParseSums(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(strings.TrimRight(line, "\r"))
		if len(f) != 2 || len(f[0]) != 64 {
			continue
		}
		if _, err := hex.DecodeString(f[0]); err != nil {
			continue
		}
		name := strings.TrimPrefix(f[1], "*")
		out[name] = strings.ToLower(f[0])
	}
	return out
}

// ServerAsset is the release asset name of the server binary for an arch.
func ServerAsset(goarch string) string { return "sentrel-server-linux-" + goarch }

// VerifyBundle verifies a signed checksum file and the hash of one binary.
// sums and sig are the contents of SHA256SUMS and SHA256SUMS.sig; the binary
// is identified by its asset name and the SHA-256 of its contents.
func VerifyBundle(pub ed25519.PublicKey, sums, sig []byte, asset, binarySHA256 string) error {
	if len(sig) == 0 {
		return errors.New("SHA256SUMS.sig is missing")
	}
	if err := VerifySignature(pub, sums, sig); err != nil {
		return err
	}
	want, ok := ParseSums(sums)[asset]
	if !ok {
		return fmt.Errorf("%s is not listed in the signed SHA256SUMS", asset)
	}
	if !strings.EqualFold(want, binarySHA256) {
		return fmt.Errorf("%s does not match the signed checksum", asset)
	}
	return nil
}

// SHA256Hex returns the hex SHA-256 of b.
func SHA256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
