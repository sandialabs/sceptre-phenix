package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"phenix/tmpl"
	"phenix/types"
)

type NTP struct{}

func (NTP) Init(...Option) error {
	return nil
}

func (NTP) Name() string {
	return "ntp"
}

func (NTP) Configure(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (NTP) PreStart(ctx context.Context, exp *types.Experiment) error {
	servers := exp.Spec.Topology().FindNodesWithLabels("ntp-server")

	if len(servers) == 0 {
		// Nothing to do if no NTP server is present in the topology.
		return nil
	}

	var (
		server = servers[0] // use first server if more than one present
		ntpDir = exp.Spec.BaseDir() + "/ntp"

		serverAddr string
	)

	ifaceName := server.Labels()["ntp-server"]

	for _, iface := range server.Network().Interfaces() {
		if strings.EqualFold(iface.Name(), ifaceName) {
			serverAddr = iface.Address()
			break
		}
	}

	if serverAddr == "" {
		return fmt.Errorf("no IP address provided for NTP server")
	}

	if err := os.MkdirAll(ntpDir, 0755); err != nil {
		return fmt.Errorf("creating experiment NTP directory path: %w", err)
	}

	// Configure topology nodes as NTP clients.
	for _, node := range exp.Spec.Topology().Nodes() {
		if _, ok := node.Labels()["ntp-server"]; ok {
			// Don't configure NTP server nodes as clients.
			continue
		}

		ntpFile := ntpDir + "/" + node.General().Hostname() + "_ntp"

		if strings.ToUpper(node.Type()) == "ROUTER" {
			if err := tmpl.CreateFileFromTemplate("ntp_linux.tmpl", serverAddr, ntpFile); err != nil {
				return fmt.Errorf("generating Router NTP script: %w", err)
			}

			switch strings.ToUpper(node.Hardware().OSType()) {
			case "MINIROUTER":
				node.AddInject(ntpFile, "/etc/ntp.conf", "", "")
			default:
				node.AddInject(ntpFile, "/opt/vyatta/etc/ntp.conf", "", "")
			}

			continue
		}

		switch strings.ToUpper(node.Hardware().OSType()) {
		case "LINUX", "RHEL", "CENTOS":
			if err := tmpl.CreateFileFromTemplate("ntp_linux.tmpl", serverAddr, ntpFile); err != nil {
				return fmt.Errorf("generating Linux NTP script: %w", err)
			}

			node.AddInject(ntpFile, "/etc/ntp.conf", "", "")
		case "WINDOWS":
			if err := tmpl.CreateFileFromTemplate("ntp_windows.tmpl", serverAddr, ntpFile); err != nil {
				return fmt.Errorf("generating Windows NTP script: %w", err)
			}

			node.AddInject(ntpFile, "/phenix/startup/25-ntp.ps1", "0755", "")
		}
	}

	return nil
}

func (NTP) PostStart(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (NTP) Running(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (NTP) Cleanup(ctx context.Context, exp *types.Experiment) error {
	return nil
}
