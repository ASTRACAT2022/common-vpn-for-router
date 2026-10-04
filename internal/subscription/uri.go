package subscription

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"common-vpn-router/internal/node"
)

func ParseNodeURI(raw string) (node.Node, error) {
	if strings.HasPrefix(strings.ToLower(raw), "vmess://") {
		return parseVMessURI(raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return node.Node{}, fmt.Errorf("invalid URI: %w", err)
	}
	name := u.Fragment
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	n := node.Node{ID: node.StableID(raw), Name: name}
	switch strings.ToLower(u.Scheme) {
	case "vless":
		n.Protocol = node.VLESS
		if u.User == nil || u.User.Username() == "" {
			return node.Node{}, errors.New("vless URI is missing UUID")
		}
		n.VLESS = &node.VLESSCredentials{UUID: u.User.Username()}
	case "trojan":
		n.Protocol = node.Trojan
		if u.User == nil {
			return node.Node{}, errors.New("trojan URI is missing password")
		}
		password, err := url.PathUnescape(u.User.String())
		if err != nil {
			return node.Node{}, errors.New("invalid trojan password encoding")
		}
		n.Trojan = &node.TrojanCredentials{Password: password}
	case "ss":
		return parseShadowsocksURI(u, raw, name)
	default:
		return node.Node{}, fmt.Errorf("%w: %q", ErrUnsupportedProtocol, u.Scheme)
	}

	address := u.Hostname()
	port, err := parsePort(u.Port())
	if err != nil {
		return node.Node{}, err
	}
	if address == "" {
		return node.Node{}, errors.New("URI is missing server address")
	}
	n.Address, n.Port = address, port
	if n.Name == "" {
		n.Name = address
	}
	q := u.Query()
	n.Transport = node.TransportOptions{
		Type: firstNonEmpty(q.Get("type"), q.Get("network"), "tcp"),
		Host: q.Get("host"), Path: q.Get("path"),
		ServiceName: firstNonEmpty(q.Get("serviceName"), q.Get("service_name")),
		HeaderType:  q.Get("headerType"), Mode: q.Get("mode"), Authority: q.Get("authority"),
	}
	if n.Protocol == node.VLESS {
		n.VLESS.Flow = q.Get("flow")
		n.VLESS.Encryption = firstNonEmpty(q.Get("encryption"), "none")
	}
	if fm := q.Get("fm"); fm != "" {
		n.FinalMask = json.RawMessage(fm)
	}
	security := strings.ToLower(q.Get("security"))
	switch security {
	case "tls":
		allowInsecure, err := optionalBool(q.Get("allowInsecure"))
		if err != nil {
			return node.Node{}, fmt.Errorf("invalid allowInsecure value: %w", err)
		}
		n.TLS = &node.TLSOptions{ServerName: firstNonEmpty(q.Get("sni"), q.Get("serverName")), ALPN: splitCSV(q.Get("alpn")), Fingerprint: q.Get("fp"), AllowInsecure: allowInsecure}
	case "reality":
		n.Reality = &node.RealityOptions{ServerName: firstNonEmpty(q.Get("sni"), q.Get("serverName")), Fingerprint: q.Get("fp"), PublicKey: firstNonEmpty(q.Get("pbk"), q.Get("publicKey")), ShortID: firstNonEmpty(q.Get("sid"), q.Get("shortId")), SpiderX: firstNonEmpty(q.Get("spx"), q.Get("spiderX"))}
	case "", "none":
		if n.Protocol == node.Trojan && security == "" {
			n.TLS = &node.TLSOptions{ServerName: firstNonEmpty(q.Get("sni"), q.Get("serverName"), address)}
		}
	default:
		return node.Node{}, fmt.Errorf("unsupported security mode %q", security)
	}
	if err := n.Validate(); err != nil {
		return node.Node{}, err
	}
	return n, nil
}

func parseVMessURI(raw string) (node.Node, error) {
	payload := strings.TrimSpace(raw[len("vmess://"):])
	decoded, err := decodeBase64(payload)
	if err != nil {
		return node.Node{}, fmt.Errorf("invalid vmess payload: %w", err)
	}
	var entry vmessJSON
	dec := json.NewDecoder(strings.NewReader(string(decoded)))
	dec.UseNumber()
	if err := dec.Decode(&entry); err != nil {
		return node.Node{}, fmt.Errorf("invalid vmess JSON: %w", err)
	}
	port, err := parsePort(entry.Port.String())
	if err != nil {
		return node.Node{}, err
	}
	aid, err := entry.AlterID.Int64()
	if err != nil && entry.AlterID.String() != "" {
		return node.Node{}, errors.New("invalid vmess alter ID")
	}
	if aid < 0 || aid > 1<<32-1 {
		return node.Node{}, errors.New("invalid vmess alter ID")
	}
	address := firstNonEmpty(entry.Address, entry.AddressAlt)
	n := node.Node{
		ID: node.StableID(raw), Name: firstNonEmpty(entry.Name, address), Protocol: node.VMess,
		Address: address, Port: port,
		VMess:     &node.VMessCredentials{UUID: entry.UUID, AlterID: uint32(aid), Cipher: firstNonEmpty(entry.Cipher, "auto")},
		Transport: node.TransportOptions{Type: firstNonEmpty(entry.Network, "tcp"), Host: entry.Host, Path: entry.Path},
	}
	switch strings.ToLower(entry.TLS) {
	case "tls", "true":
		n.TLS = &node.TLSOptions{ServerName: entry.SNI, ALPN: splitCSV(entry.ALPN), Fingerprint: entry.Fingerprint}
	case "reality":
		n.Reality = &node.RealityOptions{ServerName: entry.SNI, Fingerprint: entry.Fingerprint, PublicKey: entry.PublicKey, ShortID: entry.ShortID, SpiderX: entry.SpiderX}
	}
	if err := n.Validate(); err != nil {
		return node.Node{}, err
	}
	return n, nil
}

func parseShadowsocksURI(u *url.URL, raw, name string) (node.Node, error) {
	var method, password, address, portText string
	if u.Query().Get("plugin") != "" {
		return node.Node{}, errors.New("shadowsocks plugins are not supported yet")
	}
	schemeEnd := strings.Index(raw, "://")
	payload := raw[schemeEnd+3:]
	payload = strings.SplitN(payload, "#", 2)[0]
	payload = strings.SplitN(payload, "?", 2)[0]
	var credentials, authority string
	if at := strings.LastIndex(payload, "@"); at >= 0 {
		credentials, authority = payload[:at], payload[at+1:]
		if decoded, err := decodeBase64(credentials); err == nil && strings.Contains(string(decoded), ":") {
			credentials = string(decoded)
		} else if decoded, err := url.PathUnescape(credentials); err == nil {
			credentials = decoded
		}
	} else {
		decoded, err := decodeBase64(payload)
		if err != nil {
			return node.Node{}, errors.New("invalid shadowsocks credentials")
		}
		credentials, authority, _ = strings.Cut(string(decoded), "@")
		if authority == "" {
			return node.Node{}, errors.New("invalid shadowsocks URI payload")
		}
	}
	var ok bool
	method, password, ok = strings.Cut(credentials, ":")
	if !ok {
		return node.Node{}, errors.New("invalid shadowsocks credentials")
	}
	h, p, err := net.SplitHostPort(authority)
	if err != nil {
		return node.Node{}, fmt.Errorf("invalid shadowsocks server address: %w", err)
	}
	address, portText = h, p
	port, err := parsePort(portText)
	if err != nil {
		return node.Node{}, err
	}
	n := node.Node{ID: node.StableID(raw), Name: firstNonEmpty(name, address), Protocol: node.Shadowsocks,
		Address: address, Port: port, Shadowsocks: &node.ShadowsocksCredentials{Method: method, Password: password},
		Transport: node.TransportOptions{Type: "tcp"}}
	if err := n.Validate(); err != nil {
		return node.Node{}, err
	}
	return n, nil
}

func parsePort(text string) (uint16, error) {
	value, err := strconv.ParseUint(text, 10, 16)
	if err != nil || value == 0 {
		return 0, errors.New("server port must be between 1 and 65535")
	}
	return uint16(value), nil
}

func optionalBool(s string) (bool, error) {
	if s == "" {
		return false, nil
	}
	return strconv.ParseBool(s)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
