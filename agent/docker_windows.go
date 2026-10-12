//go:build windows

package agent

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/Microsoft/go-winio"
)

const dockerPipe = `\\.\pipe\docker_engine`

func dockerTransport() http.RoundTripper {
	to := time.Second
	c, err := winio.DialPipe(dockerPipe, &to)
	if err != nil {
		return nil
	}
	c.Close()
	return &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return winio.DialPipeContext(ctx, dockerPipe)
	}}
}
