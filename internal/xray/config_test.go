package xray

import (
	"encoding/json"
	"strings"
	"testing"

	"common-vpn-router/internal/node"
	"common-vpn-router/internal/routing"
)

func TestGenerateBuildsLocalProxiesAndSelectedOutbound(t *testing.T) {
	n := node.Node{
		ID: "node-1", Name: "Amsterdam", Protocol: node.VLESS,
		Address: "vpn.example.net", Port: 443,
		VLESS:     &node.VLESSCredentials{UUID: "123e4567-e89b-12d3-a456-426614174000", Flow: "xtls-rprx-vision"},
		Transport: node.TransportOptions{Type: "ws", Host: "cdn.example.net", Path: "/tunnel"},
		TLS:       &node.TLSOptions{ServerName: "cdn.example.net", ALPN: []string{"h2", "http/1.1"}},
	}
	content, err := Generate(n)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(content, &cfg); err != nil {
		t.Fatalf("generated invalid JSON: %v", err)
	}
	var inbounds []map[string]any
	if err := json.Unmarshal(cfg["inbounds"], &inbounds); err != nil || len(inbounds) != 3 {
		t.Fatalf("unexpected inbounds: %v, %v", inbounds, err)
	}
	var outbounds []map[string]json.RawMessage
	if err := json.Unmarshal(cfg["outbounds"], &outbounds); err != nil || len(outbounds) != 3 {
		t.Fatalf("unexpected outbounds: %v, %v", outbounds, err)
	}
	var tag, protocol string
	_ = json.Unmarshal(outbounds[0]["tag"], &tag)
	_ = json.Unmarshal(outbounds[0]["protocol"], &protocol)
	if tag != "proxy" || protocol != "vless" {
		t.Fatalf("first outbound = %s/%s, want proxy/vless", tag, protocol)
	}
	if _, ok := outbounds[0]["streamSettings"]; !ok {
		t.Fatal("generated proxy is missing stream settings")
	}
}

func TestGenerateIncludesRealitySettings(t *testing.T) {
	n := node.Node{ID: "reality", Name: "Prague", Protocol: node.VLESS, Address: "vpn.example.net", Port: 443,
		VLESS:     &node.VLESSCredentials{UUID: "123e4567-e89b-12d3-a456-426614174000"},
		Transport: node.TransportOptions{Type: "tcp"},
		Reality:   &node.RealityOptions{ServerName: "www.example.net", Fingerprint: "chrome", PublicKey: "public-key", ShortID: "abcd"},
	}
	content, err := Generate(n)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(content, &cfg); err != nil {
		t.Fatal(err)
	}
	stream := cfg.Outbounds[0].StreamSettings
	if stream.Security != "reality" || stream.RealitySettings == nil || stream.RealitySettings.ServerName != "www.example.net" {
		t.Fatalf("unexpected Reality settings: %#v", stream)
	}
}

func TestGenerateRejectsIncompleteNode(t *testing.T) {
	if _, err := Generate(node.Node{Protocol: node.VLESS}); err == nil {
		t.Fatal("expected invalid node to be rejected")
	}
}

func TestGenerateWithTunnelAndRoutingProfile(t *testing.T) {
	n := node.Node{ID: "node-1", Name: "Test", Protocol: node.Trojan, Address: "vpn.example.net", Port: 443,
		Trojan: &node.TrojanCredentials{Password: "secret"}}
	profile := &routing.Profile{Name: "Split", GlobalProxy: false, RouteOrder: []routing.Action{routing.Block, routing.Proxy, routing.Direct},
		DomainStrategy: routing.IPIfNonMatch, BlockDomains: []string{"blocked.example"}, ProxyDomains: []string{"proxy.example"}}
	content, err := GenerateWithOptions(n, Options{Tunnel: true, RoutingProfile: profile})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(content, &cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(content)), `"dns"`) {
		t.Fatal("generated config must not add DNS settings")
	}
	var inbounds []map[string]any
	if err := json.Unmarshal(cfg["inbounds"], &inbounds); err != nil {
		t.Fatal(err)
	}
	if len(inbounds) != 4 || inbounds[3]["protocol"] != "tun" {
		t.Fatalf("TUN inbound missing: %#v", inbounds)
	}
	settings := inbounds[3]["settings"].(map[string]any)
	if settings["autoOutboundsInterface"] != "auto" {
		t.Fatalf("outbound loop prevention missing: %#v", settings)
	}
	var routeConfig struct {
		DomainStrategy string           `json:"domainStrategy"`
		Rules          []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(cfg["routing"], &routeConfig); err != nil {
		t.Fatal(err)
	}
	if routeConfig.DomainStrategy != "IPIfNonMatch" || len(routeConfig.Rules) < 3 || routeConfig.Rules[len(routeConfig.Rules)-1]["outboundTag"] != "direct" {
		t.Fatalf("profile rules/fallback missing: %#v", routeConfig)
	}
}

func TestGenerateProbeOnlyUsesRequestedSOCKSPort(t *testing.T) {
	n := node.Node{ID: "probe", Name: "Test", Protocol: node.Trojan, Address: "vpn.example.net", Port: 443,
		Trojan: &node.TrojanCredentials{Password: "secret"}}
	content, err := GenerateWithOptions(n, Options{ProbeOnly: true, SocksPort: 23456})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Inbounds []Inbound `json:"inbounds"`
	}
	if err := json.Unmarshal(content, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Inbounds) != 1 || cfg.Inbounds[0].Port != 23456 {
		t.Fatalf("probe should expose only the requested temporary SOCKS port: %#v", cfg.Inbounds)
	}
}
