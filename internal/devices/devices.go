package devices

import (
	"bufio"
	"net"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Device struct {
	MAC  string   `json:"mac"`
	Name string   `json:"name,omitempty"`
	IPs  []string `json:"ips"`
}

func NormalizeMAC(value string) (string, bool) {
	address, err := net.ParseMAC(strings.TrimSpace(value))
	if err != nil || len(address) != 6 || address[0]&1 != 0 || address.String() == "00:00:00:00:00:00" {
		return "", false
	}
	return strings.ToLower(address.String()), true
}

// Discover combines OpenWrt DHCP leases and the kernel neighbour cache.
// An absent neighbour entry does not erase a still valid DHCP lease.
func Discover() []Device {
	byMAC := map[string]*Device{}
	ownerByIP := map[string]string{}
	add := func(mac, ip, name string) {
		mac, ok := NormalizeMAC(mac)
		address, err := netip.ParseAddr(ip)
		if !ok || err != nil || !address.IsValid() || address.IsLoopback() || address.IsUnspecified() || address.IsMulticast() {
			return
		}
		device := byMAC[mac]
		if device == nil {
			device = &Device{MAC: mac, IPs: []string{}}
			byMAC[mac] = device
		}
		if name != "" && name != "*" {
			device.Name = name
		}
		canonical := address.Unmap().String()
		if previous := ownerByIP[canonical]; previous != "" && previous != mac {
			old := byMAC[previous]
			for i, value := range old.IPs {
				if value == canonical {
					old.IPs = append(old.IPs[:i], old.IPs[i+1:]...)
					break
				}
			}
		}
		ownerByIP[canonical] = mac
		for _, existing := range device.IPs {
			if existing == canonical {
				return
			}
		}
		device.IPs = append(device.IPs, canonical)
	}
	if file, err := os.Open("/tmp/dhcp.leases"); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 4 {
				expires, err := strconv.ParseInt(fields[0], 10, 64)
				if err != nil || (expires != 0 && expires < time.Now().Unix()) {
					continue
				}
				add(fields[1], fields[2], fields[3])
			}
		}
		file.Close()
	}
	if file, err := os.Open("/proc/net/arp"); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 4 && fields[2] == "0x2" {
				add(fields[3], fields[0], "")
			}
		}
		file.Close()
	}
	result := make([]Device, 0, len(byMAC))
	for _, device := range byMAC {
		if len(device.IPs) == 0 {
			continue
		}
		sort.Strings(device.IPs)
		result = append(result, *device)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].MAC < result[j].MAC })
	return result
}

func Resolve(selected []string, discovered []Device) []string {
	wanted := make(map[string]bool, len(selected))
	for _, mac := range selected {
		wanted[mac] = true
	}
	var ips []string
	for _, device := range discovered {
		if wanted[device.MAC] {
			ips = append(ips, device.IPs...)
		}
	}
	sort.Strings(ips)
	return ips
}
