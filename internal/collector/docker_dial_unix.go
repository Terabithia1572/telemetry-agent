//go:build !windows

package collector

import (
	"context"
	"net"
)

// dialDocker connects to the Docker Engine unix domain socket on Linux and macOS.
func dialDocker(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", "/var/run/docker.sock")
}
