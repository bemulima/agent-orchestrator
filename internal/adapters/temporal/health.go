package temporal

import (
	"context"
	"net"
)

// HealthChecker verifies that the configured Temporal frontend is reachable.
type HealthChecker struct {
	HostPort string
}

func (HealthChecker) Name() string { return "temporal" }

func (c HealthChecker) Ping(ctx context.Context) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.HostPort)
	if err != nil {
		return err
	}
	return conn.Close()
}
