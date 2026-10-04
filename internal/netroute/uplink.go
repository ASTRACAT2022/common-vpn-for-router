package netroute

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// DefaultOutboundInterface returns the best physical IPv4 default-route
// interface. Xray must bind its upstream sockets to this device before its TUN
// split-default routes are installed, otherwise its own connections can loop
// back into the tunnel.
func DefaultOutboundInterface() (string, error) {
	output, err := exec.Command("ip", "-4", "route", "show", "default").Output()
	if err != nil {
		return "", fmt.Errorf("read IPv4 default routes: %w", err)
	}

	bestInterface := ""
	bestMetric := int(^uint(0) >> 1)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "default" {
			continue
		}
		iface, metric := "", 0
		for i := 1; i < len(fields); i++ {
			switch fields[i] {
			case "dev":
				if i+1 < len(fields) {
					iface = fields[i+1]
				}
			case "metric":
				if i+1 < len(fields) {
					if value, parseErr := strconv.Atoi(fields[i+1]); parseErr == nil {
						metric = value
					}
				}
			}
		}
		if iface == "" || iface == "commonvpn0" || strings.HasPrefix(iface, "cvprobe") {
			continue
		}
		if metric < bestMetric {
			bestInterface, bestMetric = iface, metric
		}
	}
	if bestInterface == "" {
		return "", errors.New("no physical IPv4 default-route interface found")
	}
	return bestInterface, nil
}
