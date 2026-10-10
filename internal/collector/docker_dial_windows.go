//go:build windows

package collector

import (
	"context"
	"net"

	winio "github.com/Microsoft/go-winio"
)

// dialDocker connects to the Docker Engine named pipe on Windows.
func dialDocker(ctx context.Context) (net.Conn, error) {
	return winio.DialPipeContext(ctx, `\\.\pipe\docker_engine`)
}
