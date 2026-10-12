//go:build linux

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// privScanSync installs (or refreshes) the port-scan detection rules.
func privScanSync(ctx context.Context, w io.Writer, args []string) error {
	ports, threshold, a4, a6, err := parseScanSyncArgs(args)
	if err != nil {
		return err
	}
	if !hasNft() {
		return errors.New("nftables is not installed")
	}
	if err := ensureNft(ctx); err != nil {
		return err
	}
	if err := runNftScript(ctx, buildScanScript(ports, threshold, a4, a6)); err != nil {
		return err
	}
	fmt.Fprintf(w, "port-scan detection active: %d open ports, threshold %d/min\n", len(ports), threshold)
	return nil
}

func privScanOff(ctx context.Context, w io.Writer) error {
	if !hasNft() {
		return nil
	}
	if exec.CommandContext(ctx, "nft", "list", "table", "inet", "sentrel").Run() != nil {
		return nil
	}
	if err := runNftScript(ctx, scanOffScript); err != nil {
		return err
	}
	fmt.Fprintln(w, "port-scan detection removed")
	return nil
}

func runNftScript(ctx context.Context, script string) error {
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("nft: %v: %s", err, msg)
	}
	return nil
}

// privScanList prints the flagged sources with their remaining time as JSON.
func privScanList(ctx context.Context, w io.Writer) error {
	if !hasNft() {
		return errors.New("nftables is not installed")
	}
	out := []ScanEntry{}
	for _, set := range []string{"scanners4", "scanners6"} {
		b, err := exec.CommandContext(ctx, "nft", "-j", "list", "set", "inet", "sentrel", set).Output()
		if err != nil {
			return fmt.Errorf("port-scan rules are not installed")
		}
		es, err := parseScanSet(b)
		if err != nil {
			return err
		}
		out = append(out, es...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	if len(out) > 500 {
		out = out[:500]
	}
	return json.NewEncoder(w).Encode(out)
}

// listeningTCPPorts returns the TCP ports with a listener in this network
// namespace, read straight from /proc (no process lookup, a few KB).
func listeningTCPPorts() ([]int, error) {
	seen := map[int]bool{}
	var any bool
	for _, p := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		any = true
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if port, ok := parseProcNetTCPListen(sc.Text()); ok {
				seen[port] = true
			}
		}
		f.Close()
	}
	if !any {
		return nil, errors.New("cannot read /proc/net/tcp")
	}
	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Ints(out)
	return out, nil
}
