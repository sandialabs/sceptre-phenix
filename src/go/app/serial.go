package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"phenix/tmpl"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
)

var (
	idFormat  = "%s_serial_%s_%d"
	lfFormat  = "%s_serial_%s_%s_%d"
	optFormat = "-chardev socket,id=%[1]s,path=/tmp/%[1]s,server,nowait -device pci-serial,chardev=%[1]s"

	defaultStartPort = 40500
)

type SerialConfig struct {
	Connections []SerialConnectionConfig `mapstructure:"connections"`
}

type SerialConnectionConfig struct {
	Src  string `mapstructure:"src"`
	Dst  string `mapstructure:"dst"`
	Port int    `mapstructure:"port"`
}

type Serial struct{}

func (Serial) Init(...Option) error {
	return nil
}

func (Serial) Name() string {
	return "serial"
}

func (Serial) Configure(ctx context.Context, exp *types.Experiment) error {
	// loop through nodes
	for _, node := range exp.Spec.Topology().Nodes() {
		if node.External() {
			continue
		}

		// We only care about configuring serial interfaces on Linux VMs.
		// TODO: handle rhel and centos OS types.
		if node.Hardware().OSType() != "linux" {
			continue
		}

		var serial bool

		// Loop through interface type to see if any of the interfaces are serial.
		for _, iface := range node.Network().Interfaces() {
			if iface.Type() == "serial" {
				serial = true
				break
			}
		}

		if serial {
			// update injections to include serial type (src and dst)
			serialFile := exp.Spec.BaseDir() + "/startup/" + node.General().Hostname() + "-serial.bash"

			node.AddInject(serialFile, "/etc/phenix/serial-startup.bash", "0755", "")

			node.AddInject(
				exp.Spec.BaseDir()+"/startup/serial-startup.service",
				"/etc/systemd/system/serial-startup.service",
				"", "",
			)

			node.AddInject(
				exp.Spec.BaseDir()+"/startup/symlinks/serial-startup.service",
				"/etc/systemd/system/multi-user.target.wants/serial-startup.service",
				"", "",
			)
		}
	}

	return nil
}

func (Serial) PreStart(ctx context.Context, exp *types.Experiment) error {
	// loop through nodes
	for _, node := range exp.Spec.Topology().Nodes() {
		if node.External() {
			continue
		}

		// We only care about configuring serial interfaces on Linux VMs.
		// TODO: handle rhel and centos OS types.
		if node.Hardware().OSType() != "linux" {
			continue
		}

		var serial []ifaces.NodeNetworkInterface

		// Loop through interface type to see if any of the interfaces are serial.
		for _, iface := range node.Network().Interfaces() {
			if iface.Type() == "serial" {
				serial = append(serial, iface)
			}
		}

		if serial != nil {
			startupDir := exp.Spec.BaseDir() + "/startup"

			if err := os.MkdirAll(startupDir, 0755); err != nil {
				return fmt.Errorf("creating experiment startup directory path: %w", err)
			}

			serialFile := startupDir + "/" + node.General().Hostname() + "-serial.bash"

			if err := tmpl.CreateFileFromTemplate("serial_startup.tmpl", serial, serialFile); err != nil {
				return fmt.Errorf("generating serial script: %w", err)
			}

			if err := tmpl.RestoreAsset(startupDir, "serial-startup.service"); err != nil {
				return fmt.Errorf("restoring serial-startup.service: %w", err)
			}

			symlinksDir := startupDir + "/symlinks"

			if err := os.MkdirAll(symlinksDir, 0755); err != nil {
				return fmt.Errorf("creating experiment startup symlinks directory path: %w", err)
			}

			if err := os.Symlink("../serial-startup.service", symlinksDir+"/serial-startup.service"); err != nil {
				// Ignore the error if it was for the symlinked file already existing.
				if !strings.Contains(err.Error(), "file exists") {
					return fmt.Errorf("creating symlink for serial-startup.service: %w", err)
				}
			}
		}
	}

	// Check to see if a scenario exists for this experiment and if it contains a
	// "serial" app. If so, configure serial ports according to the app config.
	for _, app := range exp.Apps() {
		if app.Name() == "serial" {
			var config SerialConfig

			if err := app.ParseMetadata(&config); err != nil {
				continue // TODO: handle this better? Like warn the user perhaps?
			}

			for i, conn := range config.Connections {
				src := exp.Spec.Topology().FindNodeByName(conn.Src)

				if src == nil {
					continue // TODO: handle this better? Like warn the user perhaps?
				}

				appendQEMUFlags(exp.Metadata.Name, src, i)

				dst := exp.Spec.Topology().FindNodeByName(conn.Dst)

				if src == nil {
					continue // TODO: handle this better? Like warn the user perhaps?
				}

				appendQEMUFlags(exp.Metadata.Name, dst, i)
			}
		}
	}

	return nil
}

func (Serial) PostStart(ctx context.Context, exp *types.Experiment) error {
	// Check to see if a scenario exists for this experiment and if it contains a
	// "serial" app. If so, configure serial ports according to the app config.
	for _, app := range exp.Apps() {
		if app.Name() == "serial" {
			var (
				schedule = exp.Status.Schedules()
				config   SerialConfig
			)

			if err := app.ParseMetadata(&config); err != nil {
				continue // TODO: handle this better? Like warn the user perhaps?
			}

			for i, conn := range config.Connections {
				var (
					logFile = fmt.Sprintf(lfFormat, exp.Metadata.Name, conn.Src, conn.Dst, i)
					srcID   = fmt.Sprintf(idFormat, exp.Metadata.Name, conn.Src, i)
					dstID   = fmt.Sprintf(idFormat, exp.Metadata.Name, conn.Dst, i)
					srcHost = schedule[conn.Src]
					dstHost = schedule[conn.Dst]
				)

				if srcHost == dstHost { // single socat process on host connecting unix sockets
					socat := fmt.Sprintf("socat -lf%s -d -d -d -d UNIX-CONNECT:/tmp/%s UNIX-CONNECT:/tmp/%s &", logFile, srcID, dstID)

					if err := mm.MeshShell(srcHost, socat); err != nil {
						return fmt.Errorf("starting socat on %s: %w", srcHost, err)
					}
				} else { // single socat process on each host connected via TCP
					port := conn.Port

					if port == 0 {
						port = defaultStartPort + i
					}

					srcSocat := fmt.Sprintf("socat -lf%s -d -d -d -d UNIX-CONNECT:/tmp/%s TCP-LISTEN:%d &", logFile, srcID, port)

					if err := mm.MeshShell(srcHost, srcSocat); err != nil {
						return fmt.Errorf("starting socat on %s: %w", srcHost, err)
					}

					dstSocat := fmt.Sprintf("socat -lf%s -d -d -d -d UNIX-CONNECT:/tmp/%s TCP-CONNECT:%s:%d &", logFile, dstID, srcHost, port)

					if err := mm.MeshShell(dstHost, dstSocat); err != nil {
						return fmt.Errorf("starting socat on %s: %w", dstHost, err)
					}
				}
			}
		}
	}

	return nil
}

func (Serial) Running(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (Serial) Cleanup(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func appendQEMUFlags(exp string, node ifaces.NodeSpec, idx int) error {
	var (
		id      = fmt.Sprintf(idFormat, exp, node.General().Hostname(), idx)
		options = fmt.Sprintf(optFormat, id)
	)

	var qemuAppend []string

	if advanced := node.Advanced(); advanced != nil {
		if v, ok := advanced["qemu-append"]; ok {
			qemuAppend = []string{v}
		}
	}

	qemuAppend = append(qemuAppend, options)
	node.AddAdvanced("qemu-append", strings.Join(qemuAppend, " "))

	return nil
}
