package subscription

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"common-vpn-router/internal/node"
	"common-vpn-router/internal/storage"
)

func TestParseSupportedURIProtocols(t *testing.T) {
	tests := []struct {
		name, uri string
		protocol  node.Protocol
	}{
		{"vless", "vless://123e4567-e89b-12d3-a456-426614174000@example.net:443?type=ws&security=tls&sni=cdn.example.net&host=cdn.example.net&path=%2Fvpn#Berlin", node.VLESS},
		{"trojan", "trojan://secret-pass@example.net:443?security=tls&sni=example.net#Frankfurt", node.Trojan},
		{"shadowsocks-sip002", "ss://YWVzLTI1Ni1nY206c2VjcmV0@example.net:8388#Tokyo", node.Shadowsocks},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseNodeURI(tt.uri)
			if err != nil {
				t.Fatalf("ParseNodeURI() error = %v", err)
			}
			if parsed.Protocol != tt.protocol {
				t.Fatalf("protocol = %q, want %q", parsed.Protocol, tt.protocol)
			}
			if err := parsed.Validate(); err != nil {
				t.Fatalf("parsed node is invalid: %v", err)
			}
		})
	}
}

func TestParseVMessURI(t *testing.T) {
	data := `{"v":"2","ps":"Helsinki","add":"vpn.example.net","port":"8443","id":"123e4567-e89b-12d3-a456-426614174000","aid":"0","scy":"auto","net":"grpc","tls":"tls","sni":"edge.example.net","path":"service"}`
	uri := "vmess://" + base64.RawStdEncoding.EncodeToString([]byte(data))
	parsed, err := ParseNodeURI(uri)
	if err != nil {
		t.Fatalf("ParseNodeURI() error = %v", err)
	}
	if parsed.Protocol != node.VMess || parsed.Transport.Type != "grpc" || parsed.TLS == nil || parsed.TLS.ServerName != "edge.example.net" {
		t.Fatalf("unexpected parsed VMess node: %#v", parsed)
	}
}

func TestParseLegacyShadowsocksURI(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:password@ss.example.net:8388"))
	parsed, err := ParseNodeURI("ss://" + payload + "#legacy")
	if err != nil {
		t.Fatalf("ParseNodeURI() error = %v", err)
	}
	if parsed.Protocol != node.Shadowsocks || parsed.Address != "ss.example.net" || parsed.Port != 8388 {
		t.Fatalf("unexpected Shadowsocks node: %#v", parsed)
	}
}

func TestParseTrojanEscapedColonPassword(t *testing.T) {
	parsed, err := ParseNodeURI("trojan://secret%3Avalue@example.net:443#colon")
	if err != nil {
		t.Fatalf("ParseNodeURI() error = %v", err)
	}
	if parsed.Trojan.Password != "secret:value" {
		t.Fatalf("password = %q, want escaped full password", parsed.Trojan.Password)
	}
}

func TestTrojanLinkDefaultsToTLS(t *testing.T) {
	parsed, err := ParseNodeURI("trojan://secret@example.net:443#secure")
	if err != nil {
		t.Fatalf("ParseNodeURI() error = %v", err)
	}
	if parsed.TLS == nil || parsed.TLS.ServerName != "example.net" {
		t.Fatalf("Trojan link did not get default TLS settings: %#v", parsed.TLS)
	}
}

func TestParseBase64Subscription(t *testing.T) {
	plain := "vless://123e4567-e89b-12d3-a456-426614174000@vless.example.net:443#One\ntrojan://secret@trojan.example.net:443#Two"
	payload := base64.RawURLEncoding.EncodeToString([]byte(plain))
	parsed, err := ParsePayload([]byte(payload))
	if err != nil {
		t.Fatalf("ParsePayload() error = %v", err)
	}
	if len(parsed.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(parsed.Nodes))
	}
}

func TestParseVMessJSONSubscription(t *testing.T) {
	payload := `[{"ps":"Oslo","add":"a.example","port":443,"id":"123e4567-e89b-12d3-a456-426614174000","aid":0,"net":"tcp","tls":"none"}]`
	parsed, err := ParsePayload([]byte(payload))
	if err != nil {
		t.Fatalf("ParsePayload() error = %v", err)
	}
	if len(parsed.Nodes) != 1 || parsed.Nodes[0].Protocol != node.VMess {
		t.Fatalf("unexpected parsed nodes: %#v", parsed.Nodes)
	}
}

func TestParseCorruptedSubscriptionFails(t *testing.T) {
	if _, err := ParsePayload([]byte("<html>not a subscription</html>")); err == nil {
		t.Fatal("expected malformed subscription error")
	}
}

func TestMalformedSupportedNodeInvalidatesWholeSubscription(t *testing.T) {
	payload := "vless://123e4567-e89b-12d3-a456-426614174000@good.example:443\nvless://broken"
	if _, err := ParsePayload([]byte(payload)); err == nil {
		t.Fatal("expected malformed supported node to reject the payload")
	}
}

func TestUnsupportedProtocolCanBeSkippedWhenSupportedNodesExist(t *testing.T) {
	payload := "hysteria2://future@example.net:443\nvless://123e4567-e89b-12d3-a456-426614174000@good.example:443"
	parsed, err := ParsePayload([]byte(payload))
	if err != nil {
		t.Fatalf("ParsePayload() error = %v", err)
	}
	if len(parsed.Nodes) != 1 || parsed.Nodes[0].Protocol != node.VLESS {
		t.Fatalf("unexpected nodes: %#v", parsed.Nodes)
	}
}

func TestRefreshKeepsLastKnownGoodSubscription(t *testing.T) {
	valid := "vless://123e4567-e89b-12d3-a456-426614174000@vpn.example.net:443#good"
	response := valid

	store, err := storage.Open(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store)
	manager.client = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return responseFor(http.StatusOK, response, nil), nil
	})}
	sub, err := manager.Add(context.Background(), "https://example.net/sub", "test")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	response = strings.Repeat("corrupted", 30)
	if _, err := manager.Refresh(context.Background(), sub.ID); err == nil {
		t.Fatal("expected malformed refresh to fail")
	}
	state := store.Snapshot()
	if len(state.Subscriptions) != 1 || state.Subscriptions[0].Nodes[0].ID != sub.Nodes[0].ID {
		t.Fatal("failed refresh replaced last known good subscription")
	}
}

func TestNotModifiedRefreshKeepsStoredData(t *testing.T) {
	const nodeURI = "vless://123e4567-e89b-12d3-a456-426614174000@vpn.example.net:443#good"
	store, err := storage.Open(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store)
	manager.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("If-None-Match") == `"v1"` {
			return responseFor(http.StatusNotModified, "", nil), nil
		}
		headers := make(http.Header)
		headers.Set("ETag", `"v1"`)
		return responseFor(http.StatusOK, nodeURI, headers), nil
	})}
	sub, err := manager.Add(context.Background(), "https://example.net/sub", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), sub.ID); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if state.Subscriptions[0].ETag != `"v1"` || len(state.Subscriptions[0].Nodes) != 1 {
		t.Fatalf("304 refresh modified stored subscription metadata: etag=%q nodes=%d", state.Subscriptions[0].ETag, len(state.Subscriptions[0].Nodes))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func responseFor(status int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
}
