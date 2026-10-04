package xray

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"common-vpn-router/internal/node"
	"common-vpn-router/internal/routing"
)

type Config struct {
	Log       LogConfig     `json:"log"`
	Inbounds  []Inbound     `json:"inbounds"`
	Outbounds []Outbound    `json:"outbounds"`
	Routing   RoutingConfig `json:"routing"`
}

type LogConfig struct {
	LogLevel string `json:"loglevel"`
}

type Inbound struct {
	Tag      string         `json:"tag"`
	Listen   string         `json:"listen,omitempty"`
	Port     uint16         `json:"port,omitempty"`
	Protocol string         `json:"protocol"`
	Settings map[string]any `json:"settings"`
	Sniffing Sniffing       `json:"sniffing"`
}

type Sniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride"`
}

type Outbound struct {
	Tag            string          `json:"tag"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings"`
	StreamSettings *StreamSettings `json:"streamSettings,omitempty"`
}

type RoutingConfig struct {
	DomainStrategy string `json:"domainStrategy"`
	Rules          []any  `json:"rules"`
}

type StreamSettings struct {
	Network         string               `json:"network,omitempty"`
	Security        string               `json:"security,omitempty"`
	TLSSettings     *TLSSettings         `json:"tlsSettings,omitempty"`
	RealitySettings *RealitySettings     `json:"realitySettings,omitempty"`
	WSSettings      *WebSocketSettings   `json:"wsSettings,omitempty"`
	GRPCSettings    *GRPCSettings        `json:"grpcSettings,omitempty"`
	HTTPSettings    *HTTPSettings        `json:"httpSettings,omitempty"`
	HTTPUpgrade     *HTTPUpgradeSettings `json:"httpupgradeSettings,omitempty"`
	XHTTPSettings   *XHTTPSettings       `json:"xhttpSettings,omitempty"`
}

type TLSSettings struct {
	ServerName    string   `json:"serverName,omitempty"`
	ALPN          []string `json:"alpn,omitempty"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
	AllowInsecure bool     `json:"allowInsecure,omitempty"`
}

type RealitySettings struct {
	ServerName  string `json:"serverName,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	PublicKey   string `json:"publicKey,omitempty"`
	ShortID     string `json:"shortId,omitempty"`
	SpiderX     string `json:"spiderX,omitempty"`
}

type WebSocketSettings struct {
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type GRPCSettings struct {
	ServiceName string `json:"serviceName,omitempty"`
	Authority   string `json:"authority,omitempty"`
	MultiMode   bool   `json:"multiMode,omitempty"`
}

type HTTPSettings struct {
	Host []string `json:"host,omitempty"`
	Path string   `json:"path,omitempty"`
}

type HTTPUpgradeSettings struct {
	Host string `json:"host,omitempty"`
	Path string `json:"path,omitempty"`
}

type XHTTPSettings struct {
	Host string `json:"host,omitempty"`
	Path string `json:"path,omitempty"`
	Mode string `json:"mode,omitempty"`
}

type vlessSettings struct {
	VNext []struct {
		Address string `json:"address"`
		Port    uint16 `json:"port"`
		Users   []struct {
			ID         string `json:"id"`
			Encryption string `json:"encryption"`
			Flow       string `json:"flow,omitempty"`
		} `json:"users"`
	} `json:"vnext"`
}

type vmessSettings struct {
	VNext []struct {
		Address string `json:"address"`
		Port    uint16 `json:"port"`
		Users   []struct {
			ID       string `json:"id"`
			AlterID  uint32 `json:"alterId"`
			Security string `json:"security"`
		} `json:"users"`
	} `json:"vnext"`
}

type serverSettings struct {
	Servers []struct {
		Address  string `json:"address"`
		Port     uint16 `json:"port"`
		Password string `json:"password,omitempty"`
		Method   string `json:"method,omitempty"`
	} `json:"servers"`
}

type Options struct {
	Tunnel            bool
	RoutingProfile    *routing.Profile
	SocksPort         uint16
	ProbeOnly         bool
	OutboundInterface string
}

const HealthSocksPort uint16 = 10810

func Generate(selected node.Node) ([]byte, error) {
	return GenerateWithOptions(selected, Options{})
}

func GenerateWithOptions(selected node.Node, options Options) ([]byte, error) {
	if err := selected.Validate(); err != nil {
		return nil, fmt.Errorf("cannot generate Xray config: %w", err)
	}
	socksPort := options.SocksPort
	if socksPort == 0 {
		socksPort = 10808
	}
	inbounds := []Inbound{{Tag: "local-socks", Listen: "127.0.0.1", Port: socksPort, Protocol: "socks", Settings: map[string]any{"auth": "noauth", "udp": true}, Sniffing: Sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}}}}
	if !options.ProbeOnly {
		inbounds = append(inbounds, Inbound{Tag: "local-http", Listen: "127.0.0.1", Port: 10809, Protocol: "http", Settings: map[string]any{}})
		inbounds = append(inbounds, Inbound{Tag: "auto-health", Listen: "127.0.0.1", Port: HealthSocksPort, Protocol: "socks", Settings: map[string]any{"auth": "noauth", "udp": false}})
	}
	config := Config{
		Log:       LogConfig{LogLevel: "warning"},
		Inbounds:  inbounds,
		Outbounds: []Outbound{},
		Routing:   RoutingConfig{DomainStrategy: "AsIs", Rules: []any{}},
	}
	if options.ProbeOnly && options.Tunnel {
		// A route-less TUN inbound lets Xray identify and bind its outbound
		// interface, so health probes do not loop into an already active TUN.
		config.Inbounds = append(config.Inbounds, Inbound{
			Tag: "probe-interface", Protocol: "tun",
			Settings: map[string]any{"name": fmt.Sprintf("cvprobe%d", socksPort), "mtu": 1500, "autoOutboundsInterface": outboundInterface(options.OutboundInterface)},
		})
	} else if options.Tunnel {
		config.Inbounds = append(config.Inbounds, Inbound{
			Tag: "router-tun", Protocol: "tun",
			Settings: map[string]any{
				"name": "commonvpn0", "mtu": 1500,
				"gateway": []string{"198.18.0.1/30", "fd00:ca:fe::1/126"},
				// Split defaults beat the router's WAN default route regardless of
				// its metric, while connected LAN routes remain more specific.
				"autoSystemRoutingTable": []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"},
				"autoOutboundsInterface": outboundInterface(options.OutboundInterface),
			},
			Sniffing: Sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}},
		})
	}
	proxy, err := generateProxy(selected)
	if err != nil {
		return nil, err
	}
	config.Outbounds = append(config.Outbounds, proxy, Outbound{Tag: "direct", Protocol: "freedom", Settings: rawJSON(map[string]any{})}, Outbound{Tag: "block", Protocol: "blackhole", Settings: rawJSON(map[string]any{})})
	if !options.ProbeOnly {
		config.Routing.Rules = append(config.Routing.Rules, map[string]any{"type": "field", "inboundTag": []string{"auto-health"}, "outboundTag": "proxy", "ruleTag": "common-auto-health"})
	}
	if options.RoutingProfile != nil {
		rules, err := options.RoutingProfile.Rules()
		if err != nil {
			return nil, fmt.Errorf("invalid routing profile: %w", err)
		}
		config.Routing.DomainStrategy = string(options.RoutingProfile.DomainStrategy)
		if config.Routing.DomainStrategy == "" {
			config.Routing.DomainStrategy = "AsIs"
		}
		for _, rule := range rules {
			config.Routing.Rules = append(config.Routing.Rules, rule)
		}
	} else {
		config.Routing.Rules = append(config.Routing.Rules, routing.Rule{Network: "tcp,udp", OutboundTag: "proxy", RuleTag: "default-proxy"})
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Xray config: %w", err)
	}
	if !json.Valid(encoded) {
		return nil, errors.New("generated Xray config is invalid JSON")
	}
	return encoded, nil
}

func outboundInterface(name string) string {
	if name == "" {
		return "auto"
	}
	return name
}

func generateProxy(n node.Node) (Outbound, error) {
	var protocol string
	var settings any
	switch n.Protocol {
	case node.VLESS:
		protocol = "vless"
		var value vlessSettings
		value.VNext = append(value.VNext, struct {
			Address string `json:"address"`
			Port    uint16 `json:"port"`
			Users   []struct {
				ID         string `json:"id"`
				Encryption string `json:"encryption"`
				Flow       string `json:"flow,omitempty"`
			} `json:"users"`
		}{Address: n.Address, Port: n.Port, Users: []struct {
			ID         string `json:"id"`
			Encryption string `json:"encryption"`
			Flow       string `json:"flow,omitempty"`
		}{{ID: n.VLESS.UUID, Encryption: "none", Flow: n.VLESS.Flow}}})
		settings = value
	case node.VMess:
		protocol = "vmess"
		cipher := n.VMess.Cipher
		if cipher == "" {
			cipher = "auto"
		}
		var value vmessSettings
		value.VNext = append(value.VNext, struct {
			Address string `json:"address"`
			Port    uint16 `json:"port"`
			Users   []struct {
				ID       string `json:"id"`
				AlterID  uint32 `json:"alterId"`
				Security string `json:"security"`
			} `json:"users"`
		}{Address: n.Address, Port: n.Port, Users: []struct {
			ID       string `json:"id"`
			AlterID  uint32 `json:"alterId"`
			Security string `json:"security"`
		}{{ID: n.VMess.UUID, AlterID: n.VMess.AlterID, Security: cipher}}})
		settings = value
	case node.Trojan:
		protocol = "trojan"
		value := serverSettings{}
		value.Servers = append(value.Servers, struct {
			Address  string `json:"address"`
			Port     uint16 `json:"port"`
			Password string `json:"password,omitempty"`
			Method   string `json:"method,omitempty"`
		}{Address: n.Address, Port: n.Port, Password: n.Trojan.Password})
		settings = value
	case node.Shadowsocks:
		protocol = "shadowsocks"
		value := serverSettings{}
		value.Servers = append(value.Servers, struct {
			Address  string `json:"address"`
			Port     uint16 `json:"port"`
			Password string `json:"password,omitempty"`
			Method   string `json:"method,omitempty"`
		}{Address: n.Address, Port: n.Port, Password: n.Shadowsocks.Password, Method: n.Shadowsocks.Method})
		settings = value
	default:
		return Outbound{}, fmt.Errorf("unsupported protocol %q", n.Protocol)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return Outbound{}, err
	}
	return Outbound{Tag: "proxy", Protocol: protocol, Settings: encoded, StreamSettings: generateStreamSettings(n)}, nil
}

func generateStreamSettings(n node.Node) *StreamSettings {
	network := strings.ToLower(n.Transport.Type)
	if network == "" || network == "raw" {
		network = "tcp"
	}
	settings := &StreamSettings{Network: network}
	switch network {
	case "ws":
		var headers map[string]string
		if n.Transport.Host != "" {
			headers = map[string]string{"Host": n.Transport.Host}
		}
		settings.WSSettings = &WebSocketSettings{Path: n.Transport.Path, Headers: headers}
	case "grpc":
		settings.GRPCSettings = &GRPCSettings{ServiceName: n.Transport.ServiceName, Authority: n.Transport.Authority, MultiMode: strings.EqualFold(n.Transport.Mode, "multi")}
	case "http":
		var hosts []string
		if n.Transport.Host != "" {
			hosts = []string{n.Transport.Host}
		}
		settings.HTTPSettings = &HTTPSettings{Host: hosts, Path: n.Transport.Path}
	case "httpupgrade":
		settings.HTTPUpgrade = &HTTPUpgradeSettings{Host: n.Transport.Host, Path: n.Transport.Path}
	case "xhttp":
		settings.XHTTPSettings = &XHTTPSettings{Host: n.Transport.Host, Path: n.Transport.Path, Mode: n.Transport.Mode}
	}
	if n.Reality != nil {
		settings.Security = "reality"
		fingerprint := n.Reality.Fingerprint
		if fingerprint == "" {
			fingerprint = "chrome"
		}
		settings.RealitySettings = &RealitySettings{ServerName: n.Reality.ServerName, Fingerprint: fingerprint, PublicKey: n.Reality.PublicKey, ShortID: n.Reality.ShortID, SpiderX: n.Reality.SpiderX}
	} else if n.TLS != nil {
		settings.Security = "tls"
		settings.TLSSettings = &TLSSettings{ServerName: n.TLS.ServerName, ALPN: n.TLS.ALPN, Fingerprint: n.TLS.Fingerprint, AllowInsecure: n.TLS.AllowInsecure}
	}
	return settings
}

func rawJSON(value any) json.RawMessage {
	b, _ := json.Marshal(value)
	return b
}
