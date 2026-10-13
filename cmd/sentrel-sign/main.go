// Command sentrel-sign signs and verifies release checksum files with Ed25519.
//
//	sentrel-sign keygen                          print a new private key (base64 PKCS8) to stdout
//	                                        and the public key (base64 PKIX) to stderr
//	sentrel-sign sign -key <file|env:NAME> FILE  write FILE.sig (base64 signature)
//	sentrel-sign verify -pub <file> FILE SIG     exit 0 when the signature is valid
//	sentrel-sign keygen-operator [-out op.key]   write an operator key pair for job signing
//	sentrel-sign sign-job --key op.key --spec <base64|file>
//	                                        print a base64 operator signature for a job spec
//
// The key argument is a file path, or "env:NAME" to read the base64 key
// from an environment variable (default for sign: env:SENTREL_RELEASE_KEY).
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"sentrel/internal/jobpolicy"
	"sentrel/internal/release"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sentrel-sign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: sentrel-sign keygen | sign -key <file|env:NAME> FILE | verify -pub <file> FILE SIG | keygen-operator [-out FILE] | sign-job --key FILE --spec <base64|file>")
	}
	switch args[0] {
	case "keygen":
		return keygen()
	case "sign":
		fs := flag.NewFlagSet("sign", flag.ContinueOnError)
		key := fs.String("key", "env:SENTREL_RELEASE_KEY", "private key file, or env:NAME")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("sign: exactly one FILE required")
		}
		return sign(*key, fs.Arg(0))
	case "keygen-operator":
		fs := flag.NewFlagSet("keygen-operator", flag.ContinueOnError)
		out := fs.String("out", "operator.key", "where to write the private key (PKCS#8 PEM)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return keygenOperator(*out)
	case "sign-job":
		fs := flag.NewFlagSet("sign-job", flag.ContinueOnError)
		key := fs.String("key", "", "operator private key file (from keygen-operator)")
		spec := fs.String("spec", "", "job spec: base64 text, or a file containing it")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *key == "" || *spec == "" {
			return errors.New("sign-job: --key and --spec are required")
		}
		sig, err := signJob(*key, *spec)
		if err != nil {
			return err
		}
		fmt.Println(sig)
		return nil
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ContinueOnError)
		pub := fs.String("pub", "", "public key file, or env:NAME")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *pub == "" || fs.NArg() != 2 {
			return errors.New("verify: -pub, FILE and SIG required")
		}
		return verify(*pub, fs.Arg(0), fs.Arg(1))
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func keygen() error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	pder, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(der))
	fmt.Fprintln(os.Stderr, "public key:", base64.StdEncoding.EncodeToString(pder))
	return nil
}

// readKey loads base64 text from a file or an env:NAME reference.
func readKey(ref string) ([]byte, error) {
	var text string
	if name, ok := strings.CutPrefix(ref, "env:"); ok {
		text = os.Getenv(name)
		if text == "" {
			return nil, fmt.Errorf("environment variable %s is empty", name)
		}
	} else {
		b, err := os.ReadFile(ref)
		if err != nil {
			return nil, err
		}
		text = string(b)
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(text))
}

func sign(keyRef, file string) error {
	der, err := readKey(keyRef)
	if err != nil {
		return err
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return fmt.Errorf("parse private key: %w", err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return errors.New("private key is not Ed25519")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
	return os.WriteFile(file+".sig", []byte(sig+"\n"), 0o644)
}

func verify(pubRef, file, sigFile string) error {
	der, err := readKey(pubRef)
	if err != nil {
		return err
	}
	pub, err := release.ParsePublicKey(base64.StdEncoding.EncodeToString(der))
	if err != nil {
		return err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	sb, err := os.ReadFile(sigFile)
	if err != nil {
		return err
	}
	if err := release.VerifySignature(pub, data, sb); err != nil {
		return err
	}
	fmt.Println("signature OK")
	return nil
}

// keygenOperator writes an Ed25519 operator key pair. The private key is a
// PKCS#8 PEM file (also readable by the browser signer); the public key is
// printed as raw base64 for the agent's --operator-key / operator_keys.
func keygenOperator(out string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite it", out)
	}
	if err := os.WriteFile(out, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return err
	}
	b64 := base64.StdEncoding.EncodeToString(pub)
	if err := os.WriteFile(out+".pub", []byte(b64+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("private key written to %s (keep it off the server)\n", out)
	fmt.Printf("public key:  %s\n", b64)
	fmt.Printf("key id:      %s\n", jobpolicy.KeyID(pub))
	fmt.Printf("install on an agent with: --write-jobs operator --operator-key %s\n", b64)
	return nil
}

// loadOperatorKey reads a PKCS#8 PEM (or bare base64 PKCS#8) Ed25519 key.
func loadOperatorKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	der := b
	if blk, _ := pem.Decode(b); blk != nil {
		der = blk.Bytes
	} else if d, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b))); err == nil {
		der = d
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not Ed25519")
	}
	return priv, nil
}

// signJob signs the exact spec bytes and returns the base64 signature.
func signJob(keyFile, spec string) (string, error) {
	priv, err := loadOperatorKey(keyFile)
	if err != nil {
		return "", err
	}
	text := spec
	if b, err := os.ReadFile(spec); err == nil {
		text = string(b)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(raw) == 0 {
		return "", errors.New("spec must be base64 (as returned by GET /jobs/{id}/spec) or a file containing it")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, raw)), nil
}
