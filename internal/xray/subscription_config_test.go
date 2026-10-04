package xray

import (
	"common-vpn-router/internal/subscription"
	"encoding/json"
	"net/url"
	"testing"
)

func TestSubscriptionFinalMaskSurvivesGeneration(t *testing.T) {
	mask := `{"tcp":[{"type":"header-custom","settings":{"clients":[[{"type":"str","packet":"OPTIONS rtsp://localhost:554/live RTSP/1.0\\r\\n"}]],"servers":[[{"type":"str","packet":"RTSP/1.0 200 OK\\r\\n"}]]}}]}`
	n, err := subscription.ParseNodeURI("vless://test@example.net:41001?security=none&type=tcp&encryption=none&fm=" + url.QueryEscape(mask))
	if err != nil {
		t.Fatal(err)
	}
	data, err := Generate(n)
	if err != nil {
		t.Fatal(err)
	}
	var c Config
	if err = json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	var got, want any
	json.Unmarshal(c.Outbounds[0].StreamSettings.FinalMask, &got)
	json.Unmarshal([]byte(mask), &want)
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatalf("finalmask lost: %s", a)
	}
}

func TestVLESSEncryptionPreserved(t *testing.T) {
	n, err := subscription.ParseNodeURI("vless://test@example.net:443?encryption=test-encryption")
	if err != nil {
		t.Fatal(err)
	}
	out, err := generateProxy(n)
	if err != nil {
		t.Fatal(err)
	}
	var settings vlessSettings
	json.Unmarshal(out.Settings, &settings)
	if settings.VNext[0].Users[0].Encryption != "test-encryption" {
		t.Fatal("encryption was overwritten")
	}
}

func TestInvalidFinalMaskRejected(t *testing.T) {
	for _, mask := range []string{"null", "[]", "broken"} {
		if _, err := subscription.ParseNodeURI("vless://test@example.net:443?fm=" + url.QueryEscape(mask)); err == nil {
			t.Fatalf("accepted %s", mask)
		}
	}
}
