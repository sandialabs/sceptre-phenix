package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mitchellh/mapstructure"

	"phenix/tmpl"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/util/pubsub"
)

const (
	tunnelConfigPartsPortOnly     = 1
	tunnelConfigPartsPortHost     = 2
	tunnelConfigPartsPortHostDest = 3

	startupViaCCAnnotation = "phenix/startup-via-cc"

	linuxHostnameInjectDst  = "/etc/phenix/startup/1_hostname-start.sh"
	linuxTimezoneInjectDst  = "/etc/phenix/startup/2_timezone-start.sh"
	linuxIfaceInjectDst     = "/etc/phenix/startup/3_interfaces-start.sh"
	linuxDomainInjectDst    = "/etc/phenix/startup/4_domain-start.sh"
	windowsStartupInjectDst = "/phenix/startup/20-startup.ps1"

	legacyWindowsStartupWrapperDst = "/phenix/phenix-startup.ps1"
	windowsSchedulerDst            = "ProgramData/Microsoft/Windows/Start Menu/Programs/Startup/startup_scheduler.cmd"
)

type Startup struct{}

type startupC2Executor string

const (
	startupC2Linux   startupC2Executor = "linux"
	startupC2Windows startupC2Executor = "windows"
)

type commandSetter interface {
	SetCommands([]string)
}

var startupMMFullPath = mm.GetMMFullPath //nolint:gochecknoglobals // overridden by tests

func (Startup) Init(...Option) error {
	return nil
}

func (Startup) Name() string {
	return "startup"
}

func (s *Startup) Configure(ctx context.Context, exp *types.Experiment) error {
	return nil
}

// startupViaCCEnabled reports whether the startup app should use C2 delivery.
func startupViaCCEnabled(node ifaces.NodeSpec) bool {
	val, ok := node.GetAnnotation(startupViaCCAnnotation)
	if !ok {
		return false
	}

	switch v := val.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "false", "0":
			return false
		default:
			return true
		}
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	default:
		return true
	}
}

// startupC2DeliveryEnabled reports whether generated startup scripts need C2 delivery.
func startupC2DeliveryEnabled(node ifaces.NodeSpec) bool {
	if node.General() != nil && node.General().DoNotBoot() != nil && *node.General().DoNotBoot() {
		return false
	}

	return startupViaCCEnabled(node) || firstDriveInjectPartition(node) == 0
}

// firstDriveInjectPartition returns the first disk inject partition, defaulting to one.
func firstDriveInjectPartition(node ifaces.NodeSpec) int {
	if node.Hardware() == nil {
		return 1
	}

	drives := node.Hardware().Drives()
	if len(drives) == 0 {
		return 1
	}

	part := drives[0].InjectPartition()
	if part == nil {
		return 1
	}

	return *part
}

// addStartupInject adds a startup app injection unless startup injection suppression is active.
func addStartupInject(node ifaces.NodeSpec, src, dst, perms string, suppress bool) {
	if suppress {
		return
	}

	node.AddInject(src, dst, perms, "")
}

// addStartupC2Script stages a generated startup script and queues its C2 commands.
func addStartupC2Script(exp *types.Experiment, node ifaces.NodeSpec, filename string, executor startupC2Executor) error {
	send, exec, relPath := startupC2Commands(exp, filename, executor)
	if err := stageStartupC2Script(filename, relPath); err != nil {
		return fmt.Errorf("staging startup C2 script for node %s: %w", node.General().Hostname(), err)
	}

	node.AddCommand(send)
	node.AddCommand(exec)

	return nil
}

// stageStartupC2Script copies a generated startup script into minimega's file path.
func stageStartupC2Script(src, relPath string) error {
	dst := startupMMFullPath(relPath)

	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("creating startup C2 script directory: %w", err)
	}

	content, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading startup script: %w", err)
	}

	if err := os.WriteFile(dst, content, 0o600); err != nil {
		return fmt.Errorf("writing startup C2 script: %w", err)
	}

	return nil
}

// startupC2Commands builds the minimega send and exec-once commands for a startup script.
func startupC2Commands(exp *types.Experiment, filename string, executor startupC2Executor) (string, string, string) {
	relPath := path.Join(exp.Spec.ExperimentName(), filepath.Base(filename))
	guestPath := path.Join("/tmp/miniccc/files", relPath)

	send := "send " + relPath

	switch executor {
	case startupC2Windows:
		return send, fmt.Sprintf("exec-once cmd /c 'powershell.exe -noprofile -executionpolicy bypass -file %s'", guestPath), relPath
	default:
		return send, "exec-once bash " + guestPath, relPath
	}
}

// removeStartupC2Commands removes previously generated startup C2 commands for idempotency.
func removeStartupC2Commands(exp *types.Experiment, node ifaces.NodeSpec) {
	setter, ok := node.(commandSetter)
	if !ok {
		return
	}

	stale := make(map[string]struct{})
	for _, filename := range startupC2ScriptNames(node.General().Hostname()) {
		for _, executor := range []startupC2Executor{startupC2Linux, startupC2Windows} {
			send, exec, _ := startupC2Commands(exp, filename, executor)
			stale[send] = struct{}{}
			stale[exec] = struct{}{}
		}
	}

	commands := node.Commands()
	filtered := make([]string, 0, len(commands))

	for _, command := range commands {
		if _, ok := stale[command]; ok {
			continue
		}

		filtered = append(filtered, command)
	}

	setter.SetCommands(filtered)
}

// startupC2ScriptNames returns the generated startup script names for a node.
func startupC2ScriptNames(hostname string) []string {
	return []string{
		hostname + "-hostname.sh",
		hostname + "-timezone.sh",
		hostname + "-interfaces.sh",
		hostname + "-domain.sh",
		hostname + "-startup.ps1",
	}
}

// removeStartupInjections removes only injections owned by the startup app.
func removeStartupInjections(node ifaces.NodeSpec) {
	removeInjectionDestinations(node, map[string]struct{}{
		linuxHostnameInjectDst:  {},
		linuxTimezoneInjectDst:  {},
		linuxIfaceInjectDst:     {},
		linuxDomainInjectDst:    {},
		windowsStartupInjectDst: {},
	})
}

// removeLegacyWindowsStartupInjections cleans stale startup-owned Windows wrapper and Start Menu injections.
// Remove this migration helper after existing environments have had enough startup runs to drop those old entries.
func removeLegacyWindowsStartupInjections(node ifaces.NodeSpec) {
	removeInjectionDestinations(node, map[string]struct{}{
		legacyWindowsStartupWrapperDst: {},
		windowsSchedulerDst:            {},
		"/" + windowsSchedulerDst:      {},
	})
}

// removeInjectionDestinations filters node injections whose destinations match dsts.
func removeInjectionDestinations(node ifaces.NodeSpec, dsts map[string]struct{}) {
	injections := node.Injections()
	filtered := make([]ifaces.NodeInjection, 0, len(injections))

	for _, injection := range injections {
		if _, ok := dsts[injection.Dst()]; ok {
			continue
		}

		filtered = append(filtered, injection)
	}

	node.SetInjections(filtered)
}

//nolint:cyclop,funlen,gocyclo,maintidx // complex logic
func (s Startup) PreStart(ctx context.Context, exp *types.Experiment) error {
	var (
		startupDir = exp.Spec.BaseDir() + "/startup"
		imageDir   = common.PhenixBase + "/images/"

		// detect duplicate IPs within a VLAN (VLAN|IP --> hostname)
		ips = make(map[string]string)
	)

	err := os.MkdirAll(startupDir, 0o750)
	if err != nil {
		return fmt.Errorf("creating experiment startup directory path: %w", err)
	}

	for _, node := range exp.Spec.Topology().Nodes() {
		// check for duplicate IPs (including any non-minimega topology nodes)
		if node.Network() != nil && node.Network().Interfaces() != nil {
			for _, iface := range node.Network().Interfaces() {
				if iface.Address() == "" {
					continue
				}

				ip := net.ParseIP(iface.Address())
				if ip == nil {
					return fmt.Errorf(
						"invalid IP %s provided for %s",
						iface.Address(),
						node.General().Hostname(),
					)
				}

				key := iface.Address()

				if util.PrivateIP(ip) {
					key = fmt.Sprintf("%s|%s", iface.VLAN(), iface.Address())
					if h, ok := ips[key]; ok {
						return fmt.Errorf(
							"duplicate private IP detected on VLAN %s: %s and %s both have %s configured",
							iface.VLAN(),
							h,
							node.General().Hostname(),
							iface.Address(),
						)
					}
				} else {
					if h, ok := ips[key]; ok {
						return fmt.Errorf(
							"duplicate public IP detected: %s and %s both have %s configured",
							h,
							node.General().Hostname(),
							iface.Address(),
						)
					}
				}

				ips[key] = node.General().Hostname()

				// Warn if a gateway is not on the interfaces subnet
				if !node.External() && iface.Gateway() != "" {
					if _, subnet, err := net.ParseCIDR(
						fmt.Sprintf("%s/%d", iface.Address(), iface.Mask()),
					); err == nil {
						if gw := net.ParseIP(iface.Gateway()); gw != nil && !subnet.Contains(gw) {
							notes.AddWarnings(ctx, false, fmt.Errorf(
								"node %q interface %q gateway %s is outside the interface subnet %s (ok only with an on-link route)",
								node.General().Hostname(), iface.Name(), iface.Gateway(), subnet,
							))
						}
					}
				}
			}
		}

		if node.External() {
			continue
		}

		// Ensure a node has at least one drive
		drives := node.Hardware().Drives()
		if len(drives) == 0 {
			return fmt.Errorf(
				"node %q has no drives defined; cannot determine disk image",
				node.General().Hostname(),
			)
		}

		// Check if user provided an absolute path to image. If not, prepend path
		// with default image path.
		imagePath := drives[0].Image()

		if !filepath.IsAbs(imagePath) {
			imagePath = imageDir + imagePath
		}

		// check if the disk image is present, if not set do not boot to true and warn user
		if _, err := os.Stat(imagePath); os.IsNotExist(err) {
			node.General().SetDoNotBoot(true)
			plog.Warn(
				plog.TypeSystem,
				"disk image not found; node will not boot",
				"node", node.General().Hostname(),
				"image", imagePath,
			)

			notes.AddWarnings(ctx, false, fmt.Errorf(
				"node %q will not boot: disk image %q not found",
				node.General().Hostname(), imagePath,
			))
		}

		useC2 := startupC2DeliveryEnabled(node)
		suppressStartupInjects := startupViaCCEnabled(node)

		removeStartupC2Commands(exp, node)
		removeLegacyWindowsStartupInjections(node)

		if suppressStartupInjects {
			removeStartupInjections(node)
		}

		// if type is router, skip it and continue
		if strings.EqualFold(node.Type(), "Router") {
			continue
		}

		// Check to see if a scenario exists for this experiment and if it
		// contains a "startup" app. If so, store it for later use
		var startupApp ifaces.ScenarioApp

		for _, app := range exp.Apps() {
			if app.Name() == "startup" {
				startupApp = app
			}
		}

		switch strings.ToLower(node.Hardware().OSType()) {
		case "linux", "rhel", "centos":
			var (
				hostnameFile = startupDir + "/" + node.General().Hostname() + "-hostname.sh"
				timezoneFile = startupDir + "/" + node.General().Hostname() + "-timezone.sh"
				ifaceFile    = startupDir + "/" + node.General().Hostname() + "-interfaces.sh"
			)

			addStartupInject(node, hostnameFile, linuxHostnameInjectDst, "0755", suppressStartupInjects)
			addStartupInject(node, timezoneFile, linuxTimezoneInjectDst, "0755", suppressStartupInjects)
			addStartupInject(node, ifaceFile, linuxIfaceInjectDst, "0755", suppressStartupInjects)

			timeZone := "Etc/UTC"

			err := tmpl.CreateFileFromTemplate(
				"linux_hostname.tmpl",
				node.General().Hostname(),
				hostnameFile,
			)
			if err != nil {
				return fmt.Errorf("generating linux hostname script: %w", err)
			}

			err = tmpl.CreateFileFromTemplate("linux_timezone.tmpl", timeZone, timezoneFile)
			if err != nil {
				return fmt.Errorf("generating linux timezone script: %w", err)
			}

			err = tmpl.CreateFileFromTemplate("linux_interfaces.tmpl", node, ifaceFile)
			if err != nil {
				return fmt.Errorf("generating linux interfaces script: %w", err)
			}

			if useC2 {
				if err := addStartupC2Script(exp, node, hostnameFile, startupC2Linux); err != nil {
					return err
				}

				if err := addStartupC2Script(exp, node, timezoneFile, startupC2Linux); err != nil {
					return err
				}

				if err := addStartupC2Script(exp, node, ifaceFile, startupC2Linux); err != nil {
					return err
				}
			}

			if startupApp != nil {
				for _, host := range startupApp.Hosts() {
					if host.Hostname() == node.General().Hostname() {
						domainFile := startupDir + "/" + node.General().Hostname() + "-domain.sh"

						addStartupInject(node, domainFile, linuxDomainInjectDst, "0755", suppressStartupInjects)

						err := tmpl.CreateFileFromTemplate(
							"linux_domain.tmpl",
							host.Metadata(),
							domainFile,
						)
						if err != nil {
							return fmt.Errorf("generating linux domain script: %w", err)
						}

						if useC2 {
							if err := addStartupC2Script(exp, node, domainFile, startupC2Linux); err != nil {
								return err
							}
						}
					}
				}
			}

		case osWindows:
			startupFile := startupDir + "/" + node.General().Hostname() + "-startup.ps1"

			addStartupInject(node, startupFile, windowsStartupInjectDst, "0755", suppressStartupInjects)

			// Temporary struct to send to the Windows Startup template.
			data := struct {
				Node     ifaces.NodeSpec
				Metadata map[string]any
			}{
				Node:     node,
				Metadata: make(map[string]any),
			}

			// If startup app exists, see if this node has a metadata entry
			// in the scenario app configuration.
			if startupApp != nil {
				for _, host := range startupApp.Hosts() {
					if host.Hostname() == node.General().Hostname() {
						data.Metadata = host.Metadata()
					}
				}
			}

			err := tmpl.CreateFileFromTemplate("windows_startup.tmpl", data, startupFile)
			if err != nil {
				return fmt.Errorf("generating windows startup script: %w", err)
			}

			if useC2 {
				if err := addStartupC2Script(exp, node, startupFile, startupC2Windows); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (Startup) PostStart(ctx context.Context, exp *types.Experiment) error {
	for _, node := range exp.Spec.Topology().Nodes() {
		if node.External() {
			continue
		}

		if annotation, ok := node.GetAnnotation("phenix/startup-autotunnel"); ok {
			var tunnels []string

			err := mapstructure.Decode(annotation, &tunnels)
			if err != nil {
				plog.Error(
					plog.TypeSystem,
					"parsing phenix/startup-autotunnel annotation",
					"exp",
					exp.Metadata.Name,
					"vm",
					node.General().Hostname(),
					"err",
					err,
				)
			} else {
				for _, config := range tunnels {
					tunnel := CreateTunnel{ //nolint:exhaustruct // partial initialization
						Experiment: exp.Metadata.Name,
						VM:         node.General().Hostname(),
						User:       "bot",
					}

					tokens := strings.Split(config, ":")

					switch len(tokens) {
					case tunnelConfigPartsPortOnly:
						tunnel.Sport = tokens[0]
						tunnel.Dhost = "127.0.0.1"
						tunnel.Dport = tokens[0]
					case tunnelConfigPartsPortHost:
						tunnel.Sport = tokens[0]
						tunnel.Dhost = "127.0.0.1"
						tunnel.Dport = tokens[1]
					case tunnelConfigPartsPortHostDest:
						tunnel.Sport = tokens[0]
						tunnel.Dhost = tokens[1]
						tunnel.Dport = tokens[2]
					default:
						plog.Error(
							plog.TypeSystem,
							"invalid phenix/startup-autotunnel annotation",
							"value",
							config,
						)
					}

					if tunnel.Sport != "" {
						go func(exp, vm string, msg CreateTunnel) {
							switch strings.ToUpper(msg.Dport) {
							case "VNC": // doesn't require miniccc agent
								pubsub.Publish("create-tunnel", msg)
							default:
								err := mm.IsC2ClientActive(mm.C2NS(exp), mm.C2VM(vm))
								if err == nil {
									pubsub.Publish("create-tunnel", msg)
								}
							}
						}(exp.Metadata.Name, node.General().Hostname(), tunnel)
					}
				}
			}
		}
	}

	return nil
}

func (Startup) Running(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (Startup) Cleanup(ctx context.Context, exp *types.Experiment) error {
	return nil
}
