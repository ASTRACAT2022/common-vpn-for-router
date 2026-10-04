package openwrt

type Adapter struct{}

func New() Adapter                 { return Adapter{} }
func (Adapter) Name() string       { return "openwrt" }
func (Adapter) ConfigDir() string  { return "/etc/common-vpn" }
func (Adapter) DataDir() string    { return "/etc/common-vpn/data" }
func (Adapter) XrayBinary() string { return "/usr/bin/xray" }
