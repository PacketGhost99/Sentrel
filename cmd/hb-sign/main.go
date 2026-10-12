// Command hb-sign signs and verifies release checksum files with Ed25519.
//
//	hb-sign keygen                          print a new private key (base64 PKCS8) to stdout
//	                                        and the public key (base64 PKIX) to stderr
//	hb-sign sign -key <file|env:NAME> FILE  write FILE.sig (base64 signature)
//	hb-sign verify -pub <file> FILE SIG     exit 0 when the signature is valid
//
// The key argument is a file path, or "env:NAME" to read the base64 key
// from an environment variable (default for sign: env:HB_RELEASE_KEY).
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hb-sign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hb-sign keygen | sign -key <file|env:NAME> FILE | verify -pub <file> FILE SIG")
	}
	switch args[0] {
	case "keygen":
		return keygen()
	case "sign":
		fs := flag.NewFlagSet("sign", flag.ContinueOnError)
		key := fs.String("key", "env:HB_RELEASE_KEY", "private key file, or env:NAME")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("sign: exactly one FILE required")
		}
		return sign(*key, fs.Arg(0))
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
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return fmt.Errorf("parse public key: %w", err)
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return errors.New("public key is not Ed25519")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	sb, err := os.ReadFile(sigFile)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sb)))
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(pub, data, sig) {
		return errors.New("signature verification FAILED")
	}
	fmt.Println("signature OK")
	return nil
}
