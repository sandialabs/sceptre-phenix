package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"phenix/tmpl"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/util/pubsub"

	"github.com/mitchellh/mapstructure"
)

type Startup struct{}

func (Startup) Init(...Option) error {
	return nil
}

func (Startup) Name() string {
	return "startup"
}

func (this *Startup) Configure(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (this Startup) PreStart(ctx context.Context, exp *types.Experiment) error {
	var (
		startupDir = exp.Spec.BaseDir() + "/startup"
		imageDir   = common.PhenixBase + "/images/"

		// detect duplicate IPs within a VLAN (VLAN|IP --> hostname)
		ips = make(map[string]string)
	)

	if err := os.MkdirAll(startupDir, 0755); err != nil {
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
					return fmt.Errorf("invalid IP %s provided for %s", iface.Address(), node.General().Hostname())
				}

				key := iface.Address()

				if util.PrivateIP(ip) {
					key = fmt.Sprintf("%s|%s", iface.VLAN(), iface.Address())
					if h, ok := ips[key]; ok {
						return fmt.Errorf("duplicate private IP detected on VLAN %s: %s and %s both have %s configured", iface.VLAN(), h, node.General().Hostname(), iface.Address())
					}
				} else {
					if h, ok := ips[key]; ok {
						return fmt.Errorf("duplicate public IP detected: %s and %s both have %s configured", h, node.General().Hostname(), iface.Address())
					}
				}

				ips[key] = node.General().Hostname()
			}
		}

		if node.External() {
			continue
		}

		// Check if user provided an absolute path to image. If not, prepend path
		// with default image path.
		imagePath := node.Hardware().Drives()[0].Image()

		if !filepath.IsAbs(imagePath) {
			imagePath = imageDir + imagePath
		}

		// check if the disk image is present, if not set do not boot to true
		if _, err := os.Stat(imagePath); os.IsNotExist(err) {
			node.General().SetDoNotBoot(true)
		}

		// if type is router, skip it and continue
		if strings.EqualFold(node.Type(), "Router") {
			continue
		}

		switch strings.ToLower(node.Hardware().OSType()) {
		case "linux", "rhel", "centos":
			var (
				hostnameFile = startupDir + "/" + node.General().Hostname() + "-hostname.sh"
				timezoneFile = startupDir + "/" + node.General().Hostname() + "-timezone.sh"
				ifaceFile    = startupDir + "/" + node.General().Hostname() + "-interfaces.sh"
			)

			node.AddInject(
				hostnameFile,
				"/etc/phenix/startup/1_hostname-start.sh",
				"0755", "",
			)

			node.AddInject(
				timezoneFile,
				"/etc/phenix/startup/2_timezone-start.sh",
				"0755", "",
			)

			node.AddInject(
				ifaceFile,
				"/etc/phenix/startup/3_interfaces-start.sh",
				"0755", "",
			)

			timeZone := "Etc/UTC"

			if err := tmpl.CreateFileFromTemplate("linux_hostname.tmpl", node.General().Hostname(), hostnameFile); err != nil {
				return fmt.Errorf("generating linux hostname script: %w", err)
			}

			if err := tmpl.CreateFileFromTemplate("linux_timezone.tmpl", timeZone, timezoneFile); err != nil {
				return fmt.Errorf("generating linux timezone script: %w", err)
			}

			if err := tmpl.CreateFileFromTemplate("linux_interfaces.tmpl", node, ifaceFile); err != nil {
				return fmt.Errorf("generating linux interfaces script: %w", err)
			}
		case "windows":
			startupFile := startupDir + "/" + node.General().Hostname() + "-startup.ps1"

			node.AddInject(
				startupFile,
				"/phenix/startup/20-startup.ps1",
				"0755", "",
			)

			if incl, ok := node.GetAnnotation("includes-phenix-startup"); !ok || incl == "false" || incl == false {
				node.AddInject(
					startupDir+"/phenix-startup.ps1",
					"/phenix/phenix-startup.ps1",
					"0755", "",
				)

				if err := tmpl.RestoreAsset(startupDir, "phenix-startup.ps1"); err != nil {
					return fmt.Errorf("restoring phenix startup script: %w", err)
				}

				node.AddInject(
					startupDir+"/startup-scheduler.cmd",
					"ProgramData/Microsoft/Windows/Start Menu/Programs/Startup/startup_scheduler.cmd",
					"0755", "",
				)

				if err := tmpl.RestoreAsset(startupDir, "startup-scheduler.cmd"); err != nil {
					return fmt.Errorf("restoring windows startup scheduler: %w", err)
				}
			}

			// Temporary struct to send to the Windows Startup template.
			data := struct {
				Node     ifaces.NodeSpec
				Metadata map[string]interface{}
			}{
				Node:     node,
				Metadata: make(map[string]interface{}),
			}

			// Check to see if a scenario exists for this experiment and if it
			// contains a "startup" app. If so, see if this node has a metadata entry
			// in the scenario app configuration.
			for _, app := range exp.Apps() {
				if app.Name() == "startup" {
					for _, host := range app.Hosts() {
						if host.Hostname() == node.General().Hostname() {
							data.Metadata = host.Metadata()
						}
					}
				}
			}

			if err := tmpl.CreateFileFromTemplate("windows_startup.tmpl", data, startupFile); err != nil {
				return fmt.Errorf("generating windows startup script: %w", err)
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

		if strings.EqualFold(node.Hardware().OSType(), "windows") {
			// Windows 10 doesn't automatically run scripts in the startup folder
			if ver, ok := node.GetAnnotation("windows-version"); ok && (ver == "10" || ver == 10) {
				_, err := mm.ExecC2Command(
					mm.C2NS(exp.Metadata.Name),
					mm.C2VM(node.General().Hostname()),
					mm.C2SkipActiveClientCheck(true),
					mm.C2Command(`powershell.exe -noprofile -executionpolicy bypass -file /phenix/phenix-startup.ps1`),
				)

				if err != nil {
					return fmt.Errorf("execute C2 command to run Windows startup script: %w", err)
				}
			}
		}

		if annotation, ok := node.GetAnnotation("phenix/startup-autotunnel"); ok {
			var tunnels []string

			if err := mapstructure.Decode(annotation, &tunnels); err != nil {
				plog.Error("parsing phenix/startup-autotunnel annotation", "exp", exp.Metadata.Name, "vm", node.General().Hostname(), "err", err)
			} else {
				for _, config := range tunnels {
					tunnel := CreateTunnel{
						Experiment: exp.Metadata.Name,
						VM:         node.General().Hostname(),
						User:       "bot",
					}

					tokens := strings.Split(config, ":")

					switch len(tokens) {
					case 1:
						tunnel.Sport = tokens[0]
						tunnel.Dhost = "127.0.0.1"
						tunnel.Dport = tokens[0]
					case 2:
						tunnel.Sport = tokens[0]
						tunnel.Dhost = "127.0.0.1"
						tunnel.Dport = tokens[1]
					case 3:
						tunnel.Sport = tokens[0]
						tunnel.Dhost = tokens[1]
						tunnel.Dport = tokens[2]
					default:
						plog.Error("invalid phenix/startup-autotunnel annotation", "value", config)
					}

					if tunnel.Sport != "" {
						go func(exp, vm string, msg CreateTunnel) {
							switch strings.ToUpper(msg.Dport) {
							case "VNC": // doesn't require miniccc agent
								pubsub.Publish("create-tunnel", msg)
							default:
								if err := mm.IsC2ClientActive(mm.C2NS(exp), mm.C2VM(vm)); err == nil {
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
