package mm

import "context"

var DefaultMM MM = new(Minimega) //nolint:gochecknoglobals // default implementation

type MM interface { //nolint:interfacebloat // legacy interface
	ReadScriptFromFile(string, string) error
	ClearNamespace(string) error

	LaunchVMs(string) error
	StartLaunchedVMs(string, ...string) error
	GetLaunchProgress(context.Context, string, []string) (int, error)

	GetVMInfo(...Option) VMs
	GetVMScreenshot(...Option) ([]byte, error)
	GetVNCInfo(...Option) (VNCInfo, error)
	StartVM(...Option) error
	StopVM(...Option) error
	RedeployVM(...Option) error
	KillVM(...Option) error
	GetVMHost(...Option) (string, error)
	GetVMHosts(...Option) map[string]string
	GetVMIPv4(...Option) ([]string, error)
	GetVMState(...Option) (string, error)
	GetVMStates(...Option) (map[string]string, error)

	SetVMTags(...Option) error

	ConnectVMInterface(...Option) error
	DisconnectVMInterface(...Option) error

	CreateBridge(...Option) error
	GetBridges(context.Context, ...string) (map[string][]string, error)
	DestroyBridge(string, ...string) error

	CreateTunnel(...Option) error
	GetTunnels(...Option) []map[string]string
	CloseTunnel(...Option) error

	StartVMCapture(...Option) error
	StopVMCapture(...Option) error
	GetExperimentCaptures(...Option) []Capture
	GetExperimentCapturesChecked(...Option) ([]Capture, error)
	GetVMCaptures(...Option) []Capture

	StartNamespaceCapture(...Option) (string, error)
	StopNamespaceCapture(...Option) ([]NamespaceCapture, error)
	GetNamespaceCaptures(...Option) ([]NamespaceCapture, error)
	GetBridgeMirrors(...Option) ([]BridgeMirror, error)

	GetClusterHosts(schedOnly, diskUsage bool) (Hosts, error)
	GetNamespaceHosts(context.Context, string) (Hosts, error)
	Headnode() string
	IsHeadnode(string) bool
	GetMMArgs() (map[string]string, error)
	GetVLANs(...Option) (map[string]int, error)
	GetNamespaces(...Option) ([]Namespace, error)
	GetVLANBlacklist(...Option) ([]int, error)
	BlacklistVLAN(int, ...Option) error
	UnblacklistVLAN(int, ...Option) error
	VLANBlacklistRemovable(...Option) (bool, error)
	ClearVLANs(...Option) error

	IsC2ClientActive(...C2Option) error
	ActiveC2Clients(string, []C2ClientRef) map[C2ClientRef]bool
	ExecC2Command(...C2Option) (string, error)
	GetC2Response(...C2Option) (string, error)
	WaitForC2Response(...C2Option) (string, error)
	ClearC2Responses(...C2Option) error

	TapVLAN(...TapOption) error
	MeshShell(string, string) error
	MeshShellResponse(context.Context, string, string) (string, error)
	MeshShellRead(context.Context, string, string) (string, error)
	MeshSend(string, string, string) error
}
