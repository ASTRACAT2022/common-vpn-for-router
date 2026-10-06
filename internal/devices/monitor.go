package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type Traffic struct {
	MAC           string   `json:"mac"`
	Name          string   `json:"name,omitempty"`
	IPs           []string `json:"ips"`
	UploadBytes   uint64   `json:"uploadBytes"`
	DownloadBytes uint64   `json:"downloadBytes"`
}

type Monitor struct {
	mu          sync.Mutex
	initialized bool
	owners      map[string]string
	base        map[string]uint64
}

const table = "commonvpn_monitor"

func runNFT(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "nft", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("nft %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (m *Monitor) Snapshot(discovered []Device) ([]Traffic, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.base == nil {
		m.base = map[string]uint64{}
	}
	if _, err := exec.LookPath("nft"); err != nil {
		return nil, errors.New("nft недоступен на этом роутере")
	}
	owners := map[string]string{}
	for _, device := range discovered {
		for _, ip := range device.IPs {
			owners[ip] = device.MAC
		}
	}
	ips := make([]string, 0, len(owners))
	for ip := range owners {
		ips = append(ips, ip)
	}
	sort.Strings(ips)
	if !m.initialized || !equalOwners(owners, m.owners) {
		if m.initialized {
			if current, err := readCounters(); err == nil {
				for ip, mac := range m.owners {
					m.base["up:"+mac] += current["up:"+ip]
					m.base["down:"+mac] += current["down:"+ip]
				}
			}
		}
		if err := replaceRules(ips); err != nil {
			return nil, err
		}
		m.owners = owners
		m.initialized = true
	}
	counts, err := readCounters()
	if err != nil {
		// OpenWrt firewall reload may clear our separate nft table.
		m.initialized = false
		return nil, err
	}
	result := make([]Traffic, 0, len(discovered))
	for _, device := range discovered {
		row := Traffic{MAC: device.MAC, Name: device.Name, IPs: device.IPs, UploadBytes: m.base["up:"+device.MAC], DownloadBytes: m.base["down:"+device.MAC]}
		for _, ip := range device.IPs {
			row.UploadBytes += counts["up:"+ip]
			row.DownloadBytes += counts["down:"+ip]
		}
		result = append(result, row)
	}
	return result, nil
}

func replaceRules(ips []string) error {
	// This table has only accept-policy counters and never decides routing.
	_, _ = runNFT("delete", "table", "inet", table)
	if _, err := runNFT("add", "table", "inet", table); err != nil {
		return err
	}
	if _, err := runNFT("add", "chain", "inet", table, "forwarded", "{", "type", "filter", "hook", "forward", "priority", "0", ";", "policy", "accept", ";", "}"); err != nil {
		return err
	}
	for _, ip := range ips {
		family := "ip"
		if address, _ := netip.ParseAddr(ip); address.Is6() {
			family = "ip6"
		}
		if _, err := runNFT("add", "rule", "inet", table, "forwarded", "oifname", "commonvpn0", family, "saddr", ip, "counter", "comment", "up:"+ip); err != nil {
			return err
		}
		if _, err := runNFT("add", "rule", "inet", table, "forwarded", "iifname", "commonvpn0", family, "daddr", ip, "counter", "comment", "down:"+ip); err != nil {
			return err
		}
	}
	return nil
}

func readCounters() (map[string]uint64, error) {
	output, err := runNFT("-j", "list", "chain", "inet", table, "forwarded")
	if err != nil {
		return nil, err
	}
	var document struct {
		Entries []struct {
			Rule *struct {
				Comment string            `json:"comment"`
				Expr    []json.RawMessage `json:"expr"`
			} `json:"rule"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(output, &document); err != nil {
		return nil, err
	}
	result := map[string]uint64{}
	for _, entry := range document.Entries {
		if entry.Rule == nil || entry.Rule.Comment == "" {
			continue
		}
		for _, expression := range entry.Rule.Expr {
			var counter struct {
				Counter *struct {
					Bytes uint64 `json:"bytes"`
				} `json:"counter"`
			}
			if json.Unmarshal(expression, &counter) == nil && counter.Counter != nil {
				result[entry.Rule.Comment] = counter.Counter.Bytes
			}
		}
	}
	return result, nil
}

func equalOwners(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for ip, mac := range a {
		if b[ip] != mac {
			return false
		}
	}
	return true
}
