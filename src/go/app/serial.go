package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"phenix/tmpl"
	"phenix/types"
	ifaces "phenix/types/interfaces"
)

const appNameSerial = "serial"

type Serial struct{}

func (Serial) Init(...Option) error {
	return nil
}

func (Serial) Name() string {
	return appNameSerial
}

func (Serial) Configure(ctx context.Context, exp *types.Experiment) error {
	// loop through nodes
	for _, node := range exp.Spec.Topology().Nodes() {
		if node.External() {
			continue
		}

		// We only care about configuring serial interfaces on Linux VMs.
		// TODO: handle rhel and centos OS types.
		if node.Hardware().OSType() != osLinux {
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
			serialFile := exp.Spec.BaseDir() + "/startup/" + node.General().
				Hostname() +
				"-serial.bash"

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
		if node.Hardware().OSType() != osLinux {
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

			err := os.MkdirAll(startupDir, 0o750)
			if err != nil {
				return fmt.Errorf("creating experiment startup directory path: %w", err)
			}

			serialFile := startupDir + "/" + node.General().Hostname() + "-serial.bash"

			err = tmpl.CreateFileFromTemplate("serial_startup.tmpl", serial, serialFile)
			if err != nil {
				return fmt.Errorf("generating serial script: %w", err)
			}

			err = tmpl.RestoreAsset(startupDir, "serial-startup.service")
			if err != nil {
				return fmt.Errorf("restoring serial-startup.service: %w", err)
			}

			symlinksDir := startupDir + "/symlinks"

			err = os.MkdirAll(symlinksDir, 0o750)
			if err != nil {
				return fmt.Errorf("creating experiment startup symlinks directory path: %w", err)
			}

			err = os.Symlink("../serial-startup.service", symlinksDir+"/serial-startup.service")
			if err != nil {
				// Ignore the error if it was for the symlinked file already existing.
				if !strings.Contains(err.Error(), "file exists") {
					return fmt.Errorf("creating symlink for serial-startup.service: %w", err)
				}
			}
		}
	}

	return nil
}

func (Serial) PostStart(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (Serial) Running(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (Serial) Cleanup(ctx context.Context, exp *types.Experiment) error {
	return nil
}
