//go:build !windows

package agent

import (
	"context"
	"net"
	"net/http"
	"os"
)

func dockerTransport() http.RoundTripper {
	sock := "/var/run/docker.sock"
	if h := os.Getenv("DOCKER_HOST"); len(h) > 7 && h[:7] == "unix://" {
		sock = h[7:]
	}
	if _, err := os.Stat(sock); err != nil {
		return nil
	}
	return &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
}
