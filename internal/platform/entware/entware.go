package entware

type Adapter struct{}

func New() Adapter                 { return Adapter{} }
func (Adapter) Name() string       { return "entware" }
func (Adapter) ConfigDir() string  { return "/opt/etc/common-vpn" }
func (Adapter) DataDir() string    { return "/opt/var/lib/common-vpn" }
func (Adapter) XrayBinary() string { return "/opt/bin/xray" }
