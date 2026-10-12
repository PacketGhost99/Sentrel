// Package pushinstall installs the agent on a remote machine over SSH
// (Linux) or WinRM (Windows Server), with no external tools.
package pushinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/masterzen/winrm"
	"golang.org/x/crypto/ssh"
)

// SSHRequest describes a Linux push install.
type SSHRequest struct {
	Host          string
	Port          int
	Username      string
	Password      string
	PrivateKey    string
	KeyPassphrase string
	SudoPassword  string // defaults to Password
	HostKey       string // optional pinned host key fingerprint (SHA256:...)
}

// Logger receives progress lines.
type Logger func(line string)

// BinaryFor returns the agent binary for os/arch.
type BinaryFor func(goos, goarch string) (io.ReadCloser, int64, error)

// SSHInstall uploads the agent binary and installer script, then runs it.
func SSHInstall(ctx context.Context, req SSHRequest, installer []byte, bin BinaryFor, installArgs []string, log Logger) (string, error) {
	var auths []ssh.AuthMethod
	if req.PrivateKey != "" {
		var signer ssh.Signer
		var err error
		if req.KeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(req.PrivateKey), []byte(req.KeyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(req.PrivateKey))
		}
		if err != nil {
			return "", fmt.Errorf("private key: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if req.Password != "" {
		auths = append(auths, ssh.Password(req.Password), ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			ans := make([]string, len(qs))
			for i := range ans {
				ans[i] = req.Password
			}
			return ans, nil
		}))
	}
	if len(auths) == 0 {
		return "", errors.New("a password or private key is required")
	}
	port := req.Port
	if port == 0 {
		port = 22
	}
	var seenKey string
	cfg := &ssh.ClientConfig{
		User: req.Username, Auth: auths, Timeout: 15 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seenKey = ssh.FingerprintSHA256(key)
			if req.HostKey != "" && !strings.EqualFold(strings.TrimSpace(req.HostKey), seenKey) {
				return fmt.Errorf("host key mismatch: got %s", seenKey)
			}
			return nil
		},
	}
	addr := net.JoinHostPort(req.Host, strconv.Itoa(port))
	log("connecting to " + addr + " as " + req.Username)
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return seenKey, fmt.Errorf("ssh: %w", err)
	}
	client := ssh.NewClient(sc, chans, reqs)
	defer client.Close()
	log("connected; host key " + seenKey)

	run := func(cmd string, stdin io.Reader) (string, error) {
		s, err := client.NewSession()
		if err != nil {
			return "", err
		}
		defer s.Close()
		var out bytes.Buffer
		s.Stdout = &out
		s.Stderr = &out
		if stdin != nil {
			s.Stdin = stdin
		}
		done := make(chan error, 1)
		go func() { done <- s.Run(cmd) }()
		select {
		case <-ctx.Done():
			_ = s.Signal(ssh.SIGKILL)
			return out.String(), ctx.Err()
		case err := <-done:
			return out.String(), err
		}
	}

	uname, err := run("uname -sm", nil)
	if err != nil {
		return seenKey, fmt.Errorf("uname: %v %s", err, uname)
	}
	f := strings.Fields(uname)
	if len(f) < 2 || f[0] != "Linux" {
		return seenKey, fmt.Errorf("unsupported remote system %q (Linux only over SSH)", strings.TrimSpace(uname))
	}
	arch := map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[f[1]]
	if arch == "" {
		return seenKey, fmt.Errorf("unsupported architecture %s", f[1])
	}
	log("remote is linux/" + arch)
	rc, size, err := bin("linux", arch)
	if err != nil {
		return seenKey, err
	}
	defer rc.Close()
	tmp := "/tmp/heartbeat-push-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if out, err := run("umask 077 && mkdir -p "+tmp, nil); err != nil {
		return seenKey, fmt.Errorf("mkdir: %v %s", err, out)
	}
	defer run("rm -rf "+tmp, nil)
	log(fmt.Sprintf("uploading agent binary (%.1f MB)", float64(size)/1e6))
	if out, err := run("cat > "+tmp+"/heartbeat-agent && chmod 700 "+tmp+"/heartbeat-agent", rc); err != nil {
		return seenKey, fmt.Errorf("upload: %v %s", err, out)
	}
	if out, err := run("cat > "+tmp+"/install.sh", bytes.NewReader(installer)); err != nil {
		return seenKey, fmt.Errorf("upload installer: %v %s", err, out)
	}
	args := append([]string{"--binary", tmp + "/heartbeat-agent"}, installArgs...)
	cmd := "sh " + tmp + "/install.sh " + shellJoin(args)
	whoami, _ := run("id -u", nil)
	var stdin io.Reader
	if strings.TrimSpace(whoami) != "0" {
		pw := req.SudoPassword
		if pw == "" {
			pw = req.Password
		}
		if pw != "" {
			cmd = "sudo -S -p '' " + cmd
			stdin = strings.NewReader(pw + "\n")
		} else {
			cmd = "sudo -n " + cmd
		}
	}
	log("running installer")
	out, err := run(cmd, stdin)
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			log("  " + l)
		}
	}
	if err != nil {
		return seenKey, fmt.Errorf("installer failed: %w", err)
	}
	return seenKey, nil
}

func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// WinRMRequest describes a Windows push install.
type WinRMRequest struct {
	Host       string
	Port       int
	HTTPS      bool
	Insecure   bool // skip certificate validation (self-signed WinRM listener)
	Username   string
	Password   string
	UseNTLM    bool
}

// WinRMInstall runs a PowerShell script on the target.
func WinRMInstall(ctx context.Context, req WinRMRequest, script string, log Logger) error {
	port := req.Port
	if port == 0 {
		port = 5985
		if req.HTTPS {
			port = 5986
		}
	}
	ep := winrm.NewEndpoint(req.Host, port, req.HTTPS, req.Insecure, nil, nil, nil, 10*time.Minute)
	params := winrm.DefaultParameters
	if req.UseNTLM || !req.HTTPS {
		params.TransportDecorator = func() winrm.Transporter { return &winrm.ClientNTLM{} }
	}
	client, err := winrm.NewClientWithParameters(ep, req.Username, req.Password, params)
	if err != nil {
		return err
	}
	log(fmt.Sprintf("connecting to WinRM %s:%d (https=%v)", req.Host, port, req.HTTPS))
	var stdout, stderr bytes.Buffer
	code, err := client.RunWithContextWithInput(ctx, winrm.Powershell(script), &stdout, &stderr, nil)
	for _, l := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			log("  " + l)
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#< CLIXML") {
			log("  ! " + l)
		}
	}
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("installer exited with code %d", code)
	}
	return nil
}
