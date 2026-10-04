package node

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

type Protocol string

const (
	VLESS       Protocol = "vless"
	VMess       Protocol = "vmess"
	Trojan      Protocol = "trojan"
	Shadowsocks Protocol = "shadowsocks"
)

type TransportOptions struct {
	Type        string `json:"type,omitempty"`
	Host        string `json:"host,omitempty"`
	Path        string `json:"path,omitempty"`
	ServiceName string `json:"serviceName,omitempty"`
	HeaderType  string `json:"headerType,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Authority   string `json:"authority,omitempty"`
}

type TLSOptions struct {
	ServerName    string   `json:"serverName,omitempty"`
	ALPN          []string `json:"alpn,omitempty"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
	AllowInsecure bool     `json:"allowInsecure,omitempty"`
}

type RealityOptions struct {
	ServerName  string `json:"serverName,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	PublicKey   string `json:"publicKey,omitempty"`
	ShortID     string `json:"shortId,omitempty"`
	SpiderX     string `json:"spiderX,omitempty"`
}

type VLESSCredentials struct {
	UUID       string `json:"uuid"`
	Encryption string `json:"encryption,omitempty"`
	Flow       string `json:"flow,omitempty"`
}

type VMessCredentials struct {
	UUID    string `json:"uuid"`
	AlterID uint32 `json:"alterId,omitempty"`
	Cipher  string `json:"cipher,omitempty"`
}

type TrojanCredentials struct {
	Password string `json:"password"`
}

type ShadowsocksCredentials struct {
	Method   string `json:"method"`
	Password string `json:"password"`
}

// Node is the protocol-neutral representation saved by the subscription layer.
// Credentials are intentionally omitted from API responses by the API DTO layer.
type Node struct {
	FinalMask   json.RawMessage         `json:"finalmask,omitempty"`
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Protocol    Protocol                `json:"protocol"`
	Address     string                  `json:"address"`
	Port        uint16                  `json:"port"`
	Transport   TransportOptions        `json:"transport,omitempty"`
	TLS         *TLSOptions             `json:"tls,omitempty"`
	Reality     *RealityOptions         `json:"reality,omitempty"`
	VLESS       *VLESSCredentials       `json:"vless,omitempty"`
	VMess       *VMessCredentials       `json:"vmess,omitempty"`
	Trojan      *TrojanCredentials      `json:"trojan,omitempty"`
	Shadowsocks *ShadowsocksCredentials `json:"shadowsocks,omitempty"`
}

func (n Node) Validate() error {
	if strings.TrimSpace(n.Name) == "" {
		return errors.New("node name is required")
	}
	if strings.TrimSpace(n.Address) == "" || strings.ContainsAny(n.Address, " \t\r\n") {
		return errors.New("node address is invalid")
	}
	if ip := net.ParseIP(strings.Trim(n.Address, "[]")); ip == nil && strings.ContainsAny(n.Address, "/:@") {
		return errors.New("node address is invalid")
	}
	if n.Port == 0 {
		return errors.New("node port must be between 1 and 65535")
	}
	switch n.Protocol {
	case VLESS:
		if n.VLESS == nil || strings.TrimSpace(n.VLESS.UUID) == "" {
			return errors.New("vless UUID is required")
		}
	case VMess:
		if n.VMess == nil || strings.TrimSpace(n.VMess.UUID) == "" {
			return errors.New("vmess UUID is required")
		}
	case Trojan:
		if n.Trojan == nil || n.Trojan.Password == "" {
			return errors.New("trojan password is required")
		}
	case Shadowsocks:
		if n.Shadowsocks == nil || strings.TrimSpace(n.Shadowsocks.Method) == "" || n.Shadowsocks.Password == "" {
			return errors.New("shadowsocks method and password are required")
		}
	default:
		return fmt.Errorf("unsupported protocol %q", n.Protocol)
	}
	if len(n.FinalMask) > 0 {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(n.FinalMask, &obj); err != nil || obj == nil {
			return errors.New("finalmask must be a JSON object")
		}
	}
	transport := strings.ToLower(n.Transport.Type)
	if transport == "" {
		transport = "tcp"
	}
	switch transport {
	case "tcp", "raw", "ws", "grpc", "httpupgrade", "xhttp", "http":
	default:
		return fmt.Errorf("unsupported transport %q", transport)
	}
	if n.Reality != nil && n.TLS != nil {
		return errors.New("reality and tls cannot both be configured")
	}
	if n.Reality != nil && n.Protocol != VLESS {
		return errors.New("reality is supported only for vless nodes")
	}
	if n.Reality != nil && n.Reality.PublicKey == "" {
		return errors.New("reality public key is required")
	}
	if n.Reality != nil && n.Reality.ServerName == "" {
		return errors.New("reality server name is required")
	}
	return nil
}

func StableID(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}
