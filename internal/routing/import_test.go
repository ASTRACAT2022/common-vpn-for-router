package routing

import (
	"encoding/base64"
	"testing"
)

func TestImportHappRoutingLink(t *testing.T) {
	payload := `{"Name":"Home","GlobalProxy":"true","RouteOrder":"block-proxy-direct","DomainStrategy":"IPIfNonMatch","DirectSites":["direct.example"],"DirectIp":["192.168.1.0/24"],"ProxySites":["proxy.example"],"ProxyIp":["203.0.113.0/24"],"BlockSites":["blocked.example"],"BlockIp":["198.51.100.3"],"RemoteDNS":"ignored"}`
	link := "happ://routing/onadd/" + base64.StdEncoding.EncodeToString([]byte(payload))
	profile, err := Import(link)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "Home" || !profile.GlobalProxy || profile.DomainStrategy != IPIfNonMatch {
		t.Fatalf("unexpected profile: %#v", profile)
	}
	if len(profile.DirectDomains) != 1 || len(profile.ProxyDomains) != 1 || len(profile.BlockDomains) != 1 {
		t.Fatalf("routing rule lists were not imported: %#v", profile)
	}
	rules, err := profile.Rules()
	if err != nil {
		t.Fatal(err)
	}
	if rules[0].OutboundTag != "block" || rules[len(rules)-1].OutboundTag != "proxy" {
		t.Fatalf("route order or GlobalProxy fallback was lost: %#v", rules)
	}
}

func TestImportCommonLinkAndGlobalDirectFallback(t *testing.T) {
	link := "common://routing/onadd/" + base64.StdEncoding.EncodeToString([]byte(`{"Name":"Lan","GlobalProxy":"false"}`))
	profile, err := Import(link)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "Lan" || profile.GlobalProxy {
		t.Fatalf("unexpected imported profile: %#v", profile)
	}
	rules, err := profile.Rules()
	if err != nil {
		t.Fatal(err)
	}
	if rules[len(rules)-1].OutboundTag != "direct" {
		t.Fatalf("fallback = %s, want direct", rules[len(rules)-1].OutboundTag)
	}
	if _, err := Import("happ://routing/onadd/not-base64!"); err == nil {
		t.Fatal("expected malformed link to fail")
	}
}

func TestImportIgnoresDNSAndGeoFields(t *testing.T) {
	profile, err := Import(`{"Name":"Rules only","GlobalProxy":true,"RemoteDns":"8.8.8.8","Geoipurl":"https://example.invalid/geoip.dat"}`)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "Rules only" || len(profile.DirectIPs)+len(profile.ProxyIPs)+len(profile.BlockIPs) != 0 {
		t.Fatalf("non-routing Happ data leaked into the rules profile: %#v", profile)
	}
}

func TestNativeRulesAreNotSilentlyDropped(t *testing.T) {
	p, err := Import(`{"name":"native","globalProxy":true,"proxyDomains":["domain:example.net"],"blockIPs":["192.0.2.1"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ProxyDomains) != 1 || len(p.BlockIPs) != 1 {
		t.Fatal("native routing rules lost")
	}
	if _, err := Import(`{"routing":{"rules":[]}}`); err == nil {
		t.Fatal("unsupported format silently accepted")
	}
}
