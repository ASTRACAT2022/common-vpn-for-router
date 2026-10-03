package platform

import (
	"os"
	"path/filepath"

	"common-vpn-router/internal/platform/entware"
	"common-vpn-router/internal/platform/openwrt"
)

type Platform interface {
	Name() string
	ConfigDir() string
	DataDir() string
	XrayBinary() string
}

func Detect() Platform {
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		return openwrt.New()
	}
	if _, err := os.Stat("/opt/etc"); err == nil {
		return entware.New()
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = filepath.Join(os.TempDir(), "common-vpn-config")
	}
	dataDir, err := os.UserCacheDir()
	if err != nil {
		dataDir = filepath.Join(os.TempDir(), "common-vpn-data")
	}
	return local{config: filepath.Join(configDir, "common-vpn"), data: filepath.Join(dataDir, "common-vpn")}
}

type local struct{ config, data string }

func (local) Name() string        { return "linux" }
func (p local) ConfigDir() string { return p.config }
func (p local) DataDir() string   { return p.data }
func (local) XrayBinary() string  { return "xray" }
