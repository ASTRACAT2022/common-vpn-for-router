package api

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"common-vpn-router/internal/config"
	"common-vpn-router/internal/model"
	"common-vpn-router/internal/node"
	"common-vpn-router/internal/storage"
	"common-vpn-router/internal/subscription"
	"common-vpn-router/internal/xray"
)

func TestAPIResponsesDoNotExposeSubscriptionOrNodeSecrets(t *testing.T) {
	store, err := storage.Open(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	secret := "very-secret-subscription-token"
	n := node.Node{ID: "node-1", Name: "Test", Protocol: node.Trojan, Address: "vpn.example.net", Port: 443,
		Trojan:    &node.TrojanCredentials{Password: "very-secret-node-password"},
		Transport: node.TransportOptions{Type: "tcp"}}
	if err := store.Update(func(state *storage.State) error {
		state.Subscriptions = append(state.Subscriptions, model.Subscription{ID: "sub-1", Name: "Test", URL: "https://sub.example.net/path/" + secret, Nodes: []node.Node{n}, UpdatedAt: time.Now()})
		state.ActiveSubscriptionID, state.SelectedNodeID = "sub-1", n.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewServer(config.Config{Version: "test"}, store, subscription.NewManager(store), xray.NewController("missing-xray", t.TempDir()+"/xray.json"), logger).Handler()
	for _, path := range []string{"/api/subscriptions", "/api/nodes", "/api/status"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, recorder.Code)
		}
		body := recorder.Body.String()
		if strings.Contains(body, secret) || strings.Contains(body, "very-secret-node-password") {
			t.Fatalf("GET %s leaked secret in response: %s", path, body)
		}
		if path == "/api/subscriptions" && (!strings.Contains(body, "sub.example.net") || !strings.Contains(body, "••••••")) {
			t.Fatalf("subscription URL is not usefully masked: %s", body)
		}
	}
}

func TestAPIRejectsCrossOriginRequests(t *testing.T) {
	store, err := storage.Open(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewServer(config.Config{}, store, subscription.NewManager(store), xray.NewController("xray", t.TempDir()+"/xray.json"), logger).Handler()
	req := httptest.NewRequest(http.MethodPost, "http://router.local/api/vpn/disconnect", nil)
	req.Host = "router.local"
	req.Header.Set("Origin", "http://evil.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestWebUIAndRoutingImport(t *testing.T) {
	store, err := storage.Open(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewServer(config.Config{}, store, subscription.NewManager(store), xray.NewController("missing-xray", t.TempDir()+"/xray.json"), logger).Handler()

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Common VPN") {
		t.Fatalf("GET / returned %d and body %q", page.Code, page.Body.String())
	}
	if page.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("Web UI response has no Content-Security-Policy")
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "function refresh") {
		t.Fatalf("GET /app.js returned %d", asset.Code)
	}
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/api/unknown", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown API path returned %d, want 404", unknown.Code)
	}

	payload := base64.StdEncoding.EncodeToString([]byte(`{"Name":"Test rules","GlobalProxy":"true"}`))
	body := `{"link":"happ://routing/onadd/` + payload + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/routing/import", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("routing import status = %d: %s", response.Code, response.Body.String())
	}
	state := store.Snapshot()
	if len(state.RoutingProfiles) != 1 || state.ActiveRoutingID != state.RoutingProfiles[0].ID || !state.RoutingProfiles[0].GlobalProxy {
		t.Fatalf("routing profile was not persisted: %#v", state)
	}
}
