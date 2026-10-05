package routing

import (
	"errors"
	"fmt"
	"net/netip"
)

type TrafficMode string

const (
	TrafficAll      TrafficMode = "all"
	TrafficTelegram TrafficMode = "telegram"
	TrafficHapp     TrafficMode = "happ"
)

// EffectiveTrafficMode preserves the behavior of states created before the
// traffic selector existed: an imported Happ profile remains active.
func EffectiveTrafficMode(mode TrafficMode, hasProfile bool) TrafficMode {
	if mode != "" {
		return mode
	}
	if hasProfile {
		return TrafficHapp
	}
	return TrafficAll
}

func ValidateTraffic(mode TrafficMode, deviceIPs []string, hasProfile bool) ([]string, error) {
	switch mode {
	case TrafficAll, TrafficTelegram:
	case TrafficHapp:
		if !hasProfile {
			return nil, errors.New("import a Happ routing profile before selecting Happ mode")
		}
	default:
		return nil, fmt.Errorf("unknown traffic mode %q", mode)
	}
	if len(deviceIPs) > 64 {
		return nil, errors.New("no more than 64 device IP addresses are allowed")
	}
	seen := make(map[string]bool, len(deviceIPs))
	clean := make([]string, 0, len(deviceIPs))
	for _, value := range deviceIPs {
		address, err := netip.ParseAddr(value)
		if err != nil || address.Zone() != "" || !address.IsValid() || address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() {
			return nil, fmt.Errorf("invalid device IP address %q; enter an individual IPv4 or IPv6 address", value)
		}
		canonical := address.Unmap().String()
		if !seen[canonical] {
			seen[canonical] = true
			clean = append(clean, canonical)
		}
	}
	return clean, nil
}

// Telegram publishes these data-center ranges at
// https://core.telegram.org/resources/cidr.txt. Domain rules also cover its
// public web links; IP rules handle MTProto connections without a hostname.
var TelegramIPs = []string{
	"91.108.56.0/22", "91.108.4.0/22", "91.108.8.0/22",
	"91.108.16.0/22", "91.108.12.0/22", "149.154.160.0/20",
	"91.105.192.0/23", "91.108.20.0/22", "185.76.151.0/24",
	"2001:b28:f23d::/48", "2001:b28:f23f::/48", "2001:67c:4e8::/48",
	"2001:b28:f23c::/48", "2a0a:f280::/32",
}

var TelegramDomains = []string{
	"domain:telegram.org", "domain:t.me", "domain:telegram.me",
	"domain:telegra.ph", "domain:telegram-cdn.org", "domain:telegram.dog",
	"domain:telesco.pe",
}
