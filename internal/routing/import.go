package routing

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidLink = errors.New("invalid routing link")

type flexibleBool bool

func (b *flexibleBool) UnmarshalJSON(data []byte) error {
	text := strings.Trim(strings.TrimSpace(string(data)), `"`)
	parsed, err := strconvParseBool(text)
	if err != nil {
		return fmt.Errorf("GlobalProxy must be true or false")
	}
	*b = flexibleBool(parsed)
	return nil
}

type happProfile struct {
	Name           string       `json:"Name"`
	GlobalProxy    flexibleBool `json:"GlobalProxy"`
	RouteOrder     string       `json:"RouteOrder"`
	DomainStrategy string       `json:"DomainStrategy"`
	DirectSites    []string     `json:"DirectSites"`
	DirectIP       []string     `json:"DirectIp"`
	ProxySites     []string     `json:"ProxySites"`
	ProxyIP        []string     `json:"ProxyIp"`
	BlockSites     []string     `json:"BlockSites"`
	BlockIP        []string     `json:"BlockIp"`
}

func Import(raw string) (Profile, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Profile{}, fmt.Errorf("%w: empty link", ErrInvalidLink)
	}
	payload := trimmed
	if strings.HasPrefix(strings.ToLower(trimmed), "happ://routing/onadd/") {
		payload = trimmed[len("happ://routing/onadd/"):]
	} else if strings.HasPrefix(strings.ToLower(trimmed), "common://routing/onadd/") {
		payload = trimmed[len("common://routing/onadd/"):]
	}
	payload = strings.TrimSpace(strings.SplitN(payload, "#", 2)[0])
	data := []byte(payload)
	if !strings.HasPrefix(payload, "{") {
		decoded, err := decodePayload(payload)
		if err != nil {
			return Profile{}, fmt.Errorf("%w: invalid Base64 payload", ErrInvalidLink)
		}
		data = decoded
	}
	var input happProfile
	if err := json.Unmarshal(data, &input); err != nil {
		return Profile{}, fmt.Errorf("%w: invalid routing JSON: %v", ErrInvalidLink, err)
	}
	order, err := parseRouteOrder(input.RouteOrder)
	if err != nil {
		return Profile{}, err
	}
	strategy := normalizeStrategy(input.DomainStrategy)
	profile := Profile{
		ID: stableID(string(data)), Name: strings.TrimSpace(input.Name), GlobalProxy: bool(input.GlobalProxy),
		RouteOrder: order, DomainStrategy: strategy,
		DirectDomains: cleanList(input.DirectSites), DirectIPs: cleanList(input.DirectIP),
		ProxyDomains: cleanList(input.ProxySites), ProxyIPs: cleanList(input.ProxyIP),
		BlockDomains: cleanList(input.BlockSites), BlockIPs: cleanList(input.BlockIP),
	}
	if profile.Name == "" {
		profile.Name = "Imported routing profile"
	}
	if err := profile.Validate(); err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrInvalidLink, err)
	}
	return profile, nil
}

func parseRouteOrder(value string) ([]Action, error) {
	if strings.TrimSpace(value) == "" {
		return []Action{Block, Proxy, Direct}, nil
	}
	parts := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return r == '-' || r == ',' || r == ' ' })
	order := make([]Action, 0, len(parts))
	for _, part := range parts {
		order = append(order, Action(part))
	}
	if _, err := normalizeOrder(order); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLink, err)
	}
	return order, nil
}

func normalizeStrategy(value string) DomainStrategy {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ipifnonmatch":
		return IPIfNonMatch
	case "ipondemand":
		return IPOnDemand
	default:
		return AsIs
	}
}

func cleanList(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func decodePayload(payload string) ([]byte, error) {
	compact := strings.Join(strings.Fields(payload), "")
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(compact); err == nil {
			return decoded, nil
		}
	}
	return nil, ErrInvalidLink
}

func stableID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func strconvParseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no", "":
		return false, nil
	default:
		return false, errors.New("invalid boolean")
	}
}
