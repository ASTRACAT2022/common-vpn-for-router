package subscription

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"common-vpn-router/internal/node"
)

var ErrNoSupportedNodes = errors.New("subscription contains no supported nodes")
var ErrUnsupportedProtocol = errors.New("unsupported subscription protocol")

// ParsePayload detects common subscription encodings, then parses supported node URIs.
func ParsePayload(payload []byte) (Parsed, error) {
	text := strings.TrimSpace(string(payload))
	if text == "" {
		return Parsed{}, errors.New("subscription is empty")
	}
	if !utf8.ValidString(text) {
		return Parsed{}, errors.New("subscription is not valid UTF-8")
	}
	if looksLikeJSON(text) {
		return parseJSON(text)
	}
	if !looksLikeURIList(text) {
		decoded, err := decodeBase64(text)
		if err != nil {
			return Parsed{}, fmt.Errorf("subscription is neither a URI list nor valid base64: %w", err)
		}
		text = strings.TrimSpace(string(decoded))
		if !utf8.ValidString(text) {
			return Parsed{}, errors.New("decoded subscription is not valid UTF-8")
		}
		if looksLikeJSON(text) {
			return parseJSON(text)
		}
	}

	var nodes []node.Node
	var lineErrors []string
	for lineNo, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		parsed, err := ParseNodeURI(line)
		if err != nil {
			if errors.Is(err, ErrUnsupportedProtocol) {
				continue
			}
			lineErrors = append(lineErrors, fmt.Sprintf("line %d: %v", lineNo+1, err))
			continue
		}
		nodes = append(nodes, parsed)
	}
	if len(lineErrors) > 0 {
		return Parsed{}, fmt.Errorf("subscription contains invalid server entries (%s)", strings.Join(lineErrors, "; "))
	}
	if len(nodes) == 0 {
		return Parsed{}, ErrNoSupportedNodes
	}
	return Parsed{Nodes: nodes}, nil
}

func looksLikeJSON(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}

func looksLikeURIList(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		return strings.Contains(line, "://")
	}
	return false
}

func decodeBase64(s string) ([]byte, error) {
	compact := strings.Join(strings.Fields(s), "")
	encodings := []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding}
	for _, enc := range encodings {
		if decoded, err := enc.DecodeString(compact); err == nil && len(decoded) > 0 {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid base64 payload")
}

type vmessJSON struct {
	Name        string      `json:"ps"`
	Address     string      `json:"add"`
	AddressAlt  string      `json:"address"`
	Port        json.Number `json:"port"`
	UUID        string      `json:"id"`
	AlterID     json.Number `json:"aid"`
	Cipher      string      `json:"scy"`
	Network     string      `json:"net"`
	Host        string      `json:"host"`
	Path        string      `json:"path"`
	TLS         string      `json:"tls"`
	SNI         string      `json:"sni"`
	ALPN        string      `json:"alpn"`
	Fingerprint string      `json:"fp"`
	PublicKey   string      `json:"pbk"`
	ShortID     string      `json:"sid"`
	SpiderX     string      `json:"spx"`
}

func parseJSON(s string) (Parsed, error) {
	var values []json.RawMessage
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
			return Parsed{}, fmt.Errorf("invalid subscription JSON: %w", err)
		}
	} else {
		values = []json.RawMessage{json.RawMessage(trimmed)}
	}
	var nodes []node.Node
	for i, raw := range values {
		var entry vmessJSON
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		if err := dec.Decode(&entry); err != nil {
			return Parsed{}, fmt.Errorf("invalid subscription JSON entry %d: %w", i+1, err)
		}
		if entry.Address == "" {
			entry.Address = entry.AddressAlt
		}
		port, err := parsePort(entry.Port.String())
		if err != nil {
			return Parsed{}, fmt.Errorf("invalid subscription JSON entry %d port: %w", i+1, err)
		}
		aid, _ := entry.AlterID.Int64()
		if aid < 0 || aid > 1<<32-1 {
			return Parsed{}, fmt.Errorf("invalid subscription JSON entry %d alter ID", i+1)
		}
		n := node.Node{
			ID: node.StableID(string(raw)), Name: entry.Name, Protocol: node.VMess,
			Address: entry.Address, Port: port,
			VMess:     &node.VMessCredentials{UUID: entry.UUID, AlterID: uint32(aid), Cipher: entry.Cipher},
			Transport: node.TransportOptions{Type: entry.Network, Host: entry.Host, Path: entry.Path},
		}
		if n.Name == "" {
			n.Name = entry.Address
		}
		if n.Transport.Type == "" {
			n.Transport.Type = "tcp"
		}
		switch strings.ToLower(entry.TLS) {
		case "tls", "true":
			n.TLS = &node.TLSOptions{ServerName: entry.SNI, ALPN: splitCSV(entry.ALPN), Fingerprint: entry.Fingerprint}
		case "reality":
			n.Reality = &node.RealityOptions{ServerName: entry.SNI, Fingerprint: entry.Fingerprint, PublicKey: entry.PublicKey, ShortID: entry.ShortID, SpiderX: entry.SpiderX}
		}
		if err := n.Validate(); err != nil {
			return Parsed{}, fmt.Errorf("invalid subscription JSON entry %d: %w", i+1, err)
		}
		nodes = append(nodes, n)
	}
	if len(nodes) == 0 {
		return Parsed{}, ErrNoSupportedNodes
	}
	return Parsed{Nodes: nodes}, nil
}

func splitCSV(value string) []string {
	var result []string
	for _, part := range strings.Split(value, ",") {
		if v := strings.TrimSpace(part); v != "" {
			result = append(result, v)
		}
	}
	return result
}
