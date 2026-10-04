package health

import (
	"context"
	"fmt"

	"common-vpn-router/internal/xray"
)

// Check sends HTTP GET health probes through the current Xray SOCKS tunnel.
func Check(ctx context.Context) error {
	return xray.CheckHTTPGet(ctx, fmt.Sprintf("127.0.0.1:%d", xray.HealthSocksPort))
}
