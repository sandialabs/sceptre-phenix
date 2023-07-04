package mm

var DefaultMM MM = new(Minimega)

type MM interface {
	ReadScriptFromFile(string) error
	ClearNamespace(string) error

	LaunchVMs(string, ...string) error
	GetLaunchProgress(string, int) (float64, error)

	GetVMInfo(...Option) VMs
	GetVMScreenshot(...Option) ([]byte, error)
	GetVNCEndpoint(...Option) (string, error)
	StartVM(...Option) error
	StopVM(...Option) error
	RedeployVM(...Option) error
	KillVM(...Option) error
	GetVMHost(...Option) (string, error)
	GetVMState(...Option) (string, error)

	ConnectVMInterface(...Option) error
	DisconnectVMInterface(...Option) error

	CreateTunnel(...Option) error
	GetTunnels(...Option) []map[string]string
	CloseTunnel(...Option) error

	StartVMCapture(...Option) error
	StopVMCapture(...Option) error
	GetExperimentCaptures(...Option) []Capture
	GetVMCaptures(...Option) []Capture

	GetClusterHosts(bool) (Hosts, error)
	Headnode() string
	IsHeadnode(string) bool
	GetVLANs(...Option) (map[string]int, error)

	IsC2ClientActive(...C2Option) error
	ExecC2Command(...C2Option) (string, error)
	GetC2Response(...C2Option) (string, error)
	WaitForC2Response(...C2Option) (string, error)
	ClearC2Responses(...C2Option) error

	TapVLAN(...TapOption) error
	MeshShell(string, string) error
	MeshSend(string, string, string) error
}
