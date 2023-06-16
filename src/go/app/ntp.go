package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"phenix/tmpl"
	"phenix/types"

	"github.com/mitchellh/mapstructure"
)

type NTPAppMetadata struct {
	DefaultSource NTPAppSource `mapstructure:"defaultSource"`
}

type NTPAppHostMetadata struct {
	Client string       `mapstructure:"client"`
	Server string       `mapstructure:"server"`
	Source NTPAppSource `mapstructure:"source"`
}

type NTPAppSource struct {
	Hostname  string `mapstructure:"hostname"`
	Interface string `mapstructure:"interface"`
	Address   string `mapstructure:"address"`
}

func (this NTPAppSource) IPAddress(exp *types.Experiment) string {
	if this.Address != "" {
		return this.Address
	}

	if this.Hostname == "" || this.Interface == "" {
		return ""
	}

	node := exp.Spec.Topology().FindNodeByName(this.Hostname)
	if node == nil {
		return ""
	}

	for _, iface := range node.Network().Interfaces() {
		if strings.EqualFold(iface.Name(), this.Interface) {
			return iface.Address()
		}
	}

	return ""
}

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
	var (
		ntpDir  = exp.Spec.BaseDir() + "/ntp"
		servers = exp.Spec.Topology().FindNodesWithLabels("ntp-server")
	)

	// If an ntp-server was specified via a node label, then continue on with the
	// legacy way of setting up NTP so as to provide backwards compatility with
	// existing topology configurations.

	if len(servers) == 0 {
		// Check to see if a scenario exists for this experiment and if it contains
		// a "ntp" app. If so, use it to configure NTP for the experiment.
		for _, app := range exp.Apps() {
			if app.Name() == "ntp" {
				var amd NTPAppMetadata
				mapstructure.Decode(app.Metadata(), &amd)

				// Might be an empty string, but that's okay... for now.
				defaultSource := amd.DefaultSource.IPAddress(exp)

				for _, host := range app.Hosts() {
					node := exp.Spec.Topology().FindNodeByName(host.Hostname())
					if node == nil {
						continue
					}

					var hmd NTPAppHostMetadata
					mapstructure.Decode(host.Metadata(), &hmd)

					var (
						source = hmd.Source.IPAddress(exp)
						cfg    = ntpDir + "/" + node.General().Hostname()
					)

					if hmd.Client != "" {
						if source == "" {
							if defaultSource == "" {
								return fmt.Errorf("no NTP source configured for host %s (and no default source configured)", host.Hostname())
							}

							source = defaultSource
						}

						switch strings.ToLower(hmd.Client) {
						case "ntp":
							if err := tmpl.CreateFileFromTemplate("ntp_linux.tmpl", source, cfg); err != nil {
								return fmt.Errorf("generating NTP client config for host %s: %w", host.Hostname(), err)
							}

							node.AddInject(cfg, "/etc/ntp.conf", "", "")
						case "systemd":
							if err := tmpl.CreateFileFromTemplate("systemd-timesyncd.tmpl", source, cfg); err != nil {
								return fmt.Errorf("generating NTP client config for host %s: %w", host.Hostname(), err)
							}

							node.AddInject(cfg, "/etc/systemd/timesyncd.conf", "", "")
						case "windows":
							if err := tmpl.CreateFileFromTemplate("ntp_windows.tmpl", source, cfg); err != nil {
								return fmt.Errorf("generating NTP client config for host %s: %w", host.Hostname(), err)
							}

							node.AddInject(cfg, "/phenix/startup/25-ntp.ps1", "0755", "")
						default:
							return fmt.Errorf("unknown NTP client type %s provided for host %s", hmd.Client, host.Hostname())
						}

						continue
					}

					if hmd.Server != "" {
						switch strings.ToLower(hmd.Server) {
						case "ntpd":
							// It's okay if `source` is an empty string here. If it is, the
							// template will generate a config for the NTP server that prefers
							// the host's clock as the source.
							if err := tmpl.CreateFileFromTemplate("ntp_linux.tmpl", source, cfg); err != nil {
								return fmt.Errorf("generating NTP server config for host %s: %w", host.Hostname(), err)
							}

							node.AddInject(cfg, "/etc/ntp.conf", "", "")
						default:
							return fmt.Errorf("unknown NTP server type %s provided for host %s", hmd.Server, host.Hostname())
						}

						continue
					}

					return fmt.Errorf("host %s missing NTP client/server type", host.Hostname())
				}
			}
		}

		return nil
	}

	var (
		server     = servers[0] // use first server if more than one present
		serverAddr string
	)

	ifaceName := server.Labels()["ntp-server"]
	serverAddr = server.Network().InterfaceAddress(ifaceName)

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

		if node.External() {
			continue
		}

		ntpFile := ntpDir + "/" + node.General().Hostname() + "_ntp"

		if strings.EqualFold(node.Type(), "router") {
			if err := tmpl.CreateFileFromTemplate("ntp_linux.tmpl", serverAddr, ntpFile); err != nil {
				return fmt.Errorf("generating Router NTP script: %w", err)
			}

			switch strings.ToLower(node.Hardware().OSType()) {
			case "minirouter":
				node.AddInject(ntpFile, "/etc/ntp.conf", "", "")
			default:
				node.AddInject(ntpFile, "/opt/vyatta/etc/ntp.conf", "", "")
			}

			continue
		}

		switch strings.ToLower(node.Hardware().OSType()) {
		case "linux", "rhel", "centos":
			if err := tmpl.CreateFileFromTemplate("ntp_linux.tmpl", serverAddr, ntpFile); err != nil {
				return fmt.Errorf("generating Linux NTP script: %w", err)
			}

			node.AddInject(ntpFile, "/etc/ntp.conf", "", "")
		case "windows":
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
