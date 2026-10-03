package config

import (
	"path/filepath"

	"common-vpn-router/internal/platform"
)

type Config struct {
	ListenAddress string
	ConfigDir     string
	DataDir       string
	XrayBinary    string
	Version       string
	Platform      string
	Tunnel        bool
}

func Defaults(version string) Config {
	p := platform.Detect()
	name := p.Name()
	return Config{ListenAddress: "127.0.0.1:8787", ConfigDir: p.ConfigDir(), DataDir: p.DataDir(), XrayBinary: p.XrayBinary(), Version: version, Platform: name, Tunnel: name == "openwrt" || name == "entware"}
}

func (c Config) StatePath() string      { return filepath.Join(c.DataDir, "state.json") }
func (c Config) XrayConfigPath() string { return filepath.Join(c.ConfigDir, "xray.json") }
