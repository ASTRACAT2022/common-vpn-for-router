package routing

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

type Action string

const (
	Block  Action = "block"
	Proxy  Action = "proxy"
	Direct Action = "direct"
)

type DomainStrategy string

const (
	AsIs         DomainStrategy = "AsIs"
	IPIfNonMatch DomainStrategy = "IPIfNonMatch"
	IPOnDemand   DomainStrategy = "IPOnDemand"
)

type Profile struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	GlobalProxy    bool           `json:"globalProxy"`
	RouteOrder     []Action       `json:"routeOrder"`
	DomainStrategy DomainStrategy `json:"domainStrategy"`
	GeoIPURL       string         `json:"geoipUrl,omitempty"`
	GeoSiteURL     string         `json:"geositeUrl,omitempty"`
	DirectDomains  []string       `json:"directDomains,omitempty"`
	DirectIPs      []string       `json:"directIPs,omitempty"`
	ProxyDomains   []string       `json:"proxyDomains,omitempty"`
	ProxyIPs       []string       `json:"proxyIPs,omitempty"`
	BlockDomains   []string       `json:"blockDomains,omitempty"`
	BlockIPs       []string       `json:"blockIPs,omitempty"`
}

type Rule struct {
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Source      []string `json:"source,omitempty"`
	Network     string   `json:"network,omitempty"`
	OutboundTag string   `json:"outboundTag"`
	RuleTag     string   `json:"ruleTag,omitempty"`
}

func (p Profile) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("routing profile name is required")
	}
	switch p.DomainStrategy {
	case "", AsIs, IPIfNonMatch, IPOnDemand:
	default:
		return fmt.Errorf("unsupported domain strategy %q", p.DomainStrategy)
	}
	if err := validateAssetURL(p.GeoIPURL); err != nil {
		return fmt.Errorf("geoip URL: %w", err)
	}
	if err := validateAssetURL(p.GeoSiteURL); err != nil {
		return fmt.Errorf("geosite URL: %w", err)
	}
	if _, err := normalizeOrder(p.RouteOrder); err != nil {
		return err
	}
	for _, group := range []struct {
		name   string
		values []string
	}{{"direct domain", p.DirectDomains}, {"proxy domain", p.ProxyDomains}, {"block domain", p.BlockDomains}} {
		for _, value := range group.values {
			if strings.TrimSpace(value) == "" || strings.ContainsAny(value, " \t\r\n") {
				return fmt.Errorf("invalid %s rule", group.name)
			}
		}
	}
	for _, group := range []struct {
		name   string
		values []string
	}{{"direct IP", p.DirectIPs}, {"proxy IP", p.ProxyIPs}, {"block IP", p.BlockIPs}} {
		for _, value := range group.values {
			if !validIPRule(strings.TrimSpace(value)) {
				return fmt.Errorf("invalid %s rule %q", group.name, value)
			}
		}
	}
	return nil
}

func (p Profile) Rules() ([]Rule, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	order, _ := normalizeOrder(p.RouteOrder)
	var rules []Rule
	for _, action := range order {
		domains, ips := p.groups(action)
		tag := string(action)
		if len(domains) > 0 {
			rules = append(rules, Rule{Domain: domains, OutboundTag: tag, RuleTag: string(action) + "-domains"})
		}
		if len(ips) > 0 {
			rules = append(rules, Rule{IP: ips, OutboundTag: tag, RuleTag: string(action) + "-ips"})
		}
	}
	fallback := Direct
	if p.GlobalProxy {
		fallback = Proxy
	}
	rules = append(rules, Rule{Network: "tcp,udp", OutboundTag: string(fallback), RuleTag: "profile-fallback"})
	return rules, nil
}

func (p Profile) groups(action Action) ([]string, []string) {
	switch action {
	case Block:
		return p.BlockDomains, p.BlockIPs
	case Proxy:
		return p.ProxyDomains, p.ProxyIPs
	default:
		return p.DirectDomains, p.DirectIPs
	}
}

func normalizeOrder(order []Action) ([]Action, error) {
	if len(order) == 0 {
		return []Action{Block, Proxy, Direct}, nil
	}
	seen := map[Action]bool{}
	for _, action := range order {
		if action != Block && action != Proxy && action != Direct {
			return nil, fmt.Errorf("unknown routing action %q", action)
		}
		if seen[action] {
			return nil, fmt.Errorf("routing action %q appears more than once", action)
		}
		seen[action] = true
	}
	if len(seen) != 3 {
		return nil, errors.New("route order must contain block, proxy, and direct")
	}
	return order, nil
}

func validIPRule(value string) bool {
	if strings.HasPrefix(value, "geoip:") {
		return len(strings.TrimPrefix(value, "geoip:")) > 0
	}
	if net.ParseIP(value) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(value)
	return err == nil
}
