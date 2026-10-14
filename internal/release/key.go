// Package release holds the trust root and the verification logic for signed
// Sentrel releases. The same code is used by the server when it stages an
// update, by the root updater when it applies one, and by sentrel-sign.
package release

// Repo is the GitHub repository that publishes releases. It is the only place
// the slug is defined.
const Repo = "XEDigital/Sentrel"

// publicKeyB64 is the release signing key (Ed25519, base64 PKIX). It is the
// trust root for self-updates. It is compiled in and cannot be replaced by an
// environment variable, a setting or a flag; see trust_prod.go and
// trust_testkey.go.
const publicKeyB64 = "MCowBQYDK2VwAyEAjpb1iz/cseAOyJrruF3jlq8TNN8rTf8ANYf+evXwn+8="
