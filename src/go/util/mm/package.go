package mm

import (
	"time"

	"phenix/util/freshness"
)

func ReadScriptFromFile(ns, filename string) error {
	return DefaultMM.ReadScriptFromFile(ns, filename)
}

// ReadC2ScriptFromFile is ReadScriptFromFile for an experiment's script of cc
// commands, which sets `cc filter` for each VM in turn. The namespace's cc lock
// (see ccLocks) is held while it runs, so phenix's own cc commands cannot pick
// up one of its filters or clear one mid-script.
func ReadC2ScriptFromFile(ns, filename string) error {
	defer ccLocks.lock(ns)()

	return DefaultMM.ReadScriptFromFile(ns, filename)
}

func ClearNamespace(ns string) error {
	return DefaultMM.ClearNamespace(ns)
}

func LaunchVMs(ns string, start ...string) error {
	return DefaultMM.LaunchVMs(ns, start...)
}

func GetLaunchProgress(ns string, expected int) (float64, error) {
	return DefaultMM.GetLaunchProgress(ns, expected)
}

func GetVMInfo(opts ...Option) VMs {
	return DefaultMM.GetVMInfo(opts...)
}

// vmInfoObserver is the optional part of MM that reports when VM info was
// read and whether minimega answered. It is kept out of MM so the stand-ins
// tests use for MM need not implement it.
type vmInfoObserver interface {
	GetVMInfoObserved(...Option) (VMs, freshness.Observation)
}

// GetVMInfoObserved is GetVMInfo, also reporting when the VMs were read and
// whether minimega answered for all of them. An MM that cannot tell is taken
// to have answered now.
func GetVMInfoObserved(opts ...Option) (VMs, freshness.Observation) {
	if o, ok := DefaultMM.(vmInfoObserver); ok {
		return o.GetVMInfoObserved(opts...)
	}

	at := time.Now()

	return DefaultMM.GetVMInfo(opts...), freshness.Observed(at)
}

func GetVMScreenshot(opts ...Option) ([]byte, error) {
	return DefaultMM.GetVMScreenshot(opts...)
}

func GetVNCEndpoint(opts ...Option) (string, error) {
	return DefaultMM.GetVNCEndpoint(opts...)
}

func StartVM(opts ...Option) error {
	return DefaultMM.StartVM(opts...)
}

func StopVM(opts ...Option) error {
	return DefaultMM.StopVM(opts...)
}

func RedeployVM(opts ...Option) error {
	return DefaultMM.RedeployVM(opts...)
}

func KillVM(opts ...Option) error {
	return DefaultMM.KillVM(opts...)
}

func GetVMHost(opts ...Option) (string, error) {
	return DefaultMM.GetVMHost(opts...)
}

func GetVMHosts(opts ...Option) map[string]string {
	return DefaultMM.GetVMHosts(opts...)
}

func GetVMIPv4(opts ...Option) ([]string, error) {
	return DefaultMM.GetVMIPv4(opts...)
}

func GetVMState(opts ...Option) (string, error) {
	return DefaultMM.GetVMState(opts...)
}

func GetVMStates(opts ...Option) (map[string]string, error) {
	return DefaultMM.GetVMStates(opts...)
}

func SetVMTags(opts ...Option) error {
	return DefaultMM.SetVMTags(opts...)
}

func ConnectVMInterface(opts ...Option) error {
	return DefaultMM.ConnectVMInterface(opts...)
}

func DisconnectVMInterface(opts ...Option) error {
	return DefaultMM.DisconnectVMInterface(opts...)
}

func CreateBridge(opts ...Option) error {
	return DefaultMM.CreateBridge(opts...)
}

func CreateTunnel(opts ...Option) error {
	return DefaultMM.CreateTunnel(opts...)
}

func GetTunnels(opts ...Option) []map[string]string {
	return DefaultMM.GetTunnels(opts...)
}

func CloseTunnel(opts ...Option) error {
	return DefaultMM.CloseTunnel(opts...)
}

func StartVMCapture(opts ...Option) error {
	return DefaultMM.StartVMCapture(opts...)
}

func StopVMCapture(opts ...Option) error {
	return DefaultMM.StopVMCapture(opts...)
}

func GetExperimentCaptures(opts ...Option) []Capture {
	return DefaultMM.GetExperimentCaptures(opts...)
}

func GetVMCaptures(opts ...Option) []Capture {
	return DefaultMM.GetVMCaptures(opts...)
}

func GetClusterHosts(schedOnly bool) (Hosts, error) {
	return DefaultMM.GetClusterHosts(schedOnly)
}

// clusterHostsObserver is the optional part of MM that reports when the
// cluster hosts were read; see vmInfoObserver.
type clusterHostsObserver interface {
	GetClusterHostsObserved(bool) (Hosts, freshness.Observation, error)
}

// GetClusterHostsObserved is GetClusterHosts, also reporting when the hosts
// were read from minimega. An MM that cannot tell is taken to have read them
// now.
func GetClusterHostsObserved(schedOnly bool) (Hosts, freshness.Observation, error) {
	if o, ok := DefaultMM.(clusterHostsObserver); ok {
		return o.GetClusterHostsObserved(schedOnly)
	}

	at := time.Now()

	hosts, err := DefaultMM.GetClusterHosts(schedOnly)
	if err != nil {
		return hosts, freshness.Observation{}, err //nolint:wrapcheck // as GetClusterHosts
	}

	return hosts, freshness.Observed(at), nil
}

func GetNamespaceHosts(ns string) (Hosts, error) {
	return DefaultMM.GetNamespaceHosts(ns)
}

func Headnode() string {
	return DefaultMM.Headnode()
}

func IsHeadnode(node string) bool {
	return DefaultMM.IsHeadnode(node)
}

func GetMMArgs() (map[string]string, error) {
	return DefaultMM.GetMMArgs()
}

func GetVLANs(opts ...Option) (map[string]int, error) {
	return DefaultMM.GetVLANs(opts...)
}

func IsC2ClientActive(opts ...C2Option) error {
	return DefaultMM.IsC2ClientActive(opts...)
}

func ActiveC2Clients(ns string, refs []C2ClientRef) map[C2ClientRef]bool {
	return DefaultMM.ActiveC2Clients(ns, refs)
}

func ExecC2Command(opts ...C2Option) (string, error) {
	return DefaultMM.ExecC2Command(opts...)
}

func GetC2Response(opts ...C2Option) (string, error) {
	return DefaultMM.GetC2Response(opts...)
}

func WaitForC2Response(opts ...C2Option) (string, error) {
	return DefaultMM.WaitForC2Response(opts...)
}

func ClearC2Responses(opts ...C2Option) error {
	return DefaultMM.ClearC2Responses(opts...)
}

func TapVLAN(opts ...TapOption) error {
	return DefaultMM.TapVLAN(opts...)
}

func MeshShell(host, cmd string) error {
	return DefaultMM.MeshShell(host, cmd)
}

func MeshShellResponse(host, cmd string) (string, error) {
	return DefaultMM.MeshShellResponse(host, cmd)
}

func MeshSend(ns, host, command string) error {
	return DefaultMM.MeshSend(ns, host, command)
}
