package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mitchellh/mapstructure"

	"phenix/tmpl"
	"phenix/types"
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

func (s NTPAppSource) IPAddress(exp *types.Experiment) string {
	if s.Address != "" {
		return s.Address
	}

	if s.Hostname == "" || s.Interface == "" {
		return ""
	}

	node := exp.Spec.Topology().FindNodeByName(s.Hostname)
	if node == nil {
		return ""
	}

	for _, iface := range node.Network().Interfaces() {
		if strings.EqualFold(iface.Name(), s.Interface) {
			return iface.Address()
		}
	}

	return ""
}

type ntpTypeConfig struct {
	tmpl string
	dest string
	mode string
}

// NTPTemplateData is the data passed to NTP configuration templates.
// Source is the upstream NTP server IP address (empty string means use the
// local clock). Server controls whether the config should allow other hosts to
// use this VM as an NTP source.
type NTPTemplateData struct {
	Source string
	Server bool
}

type NTP struct{}

func (NTP) Init(...Option) error {
	return nil
}

func (NTP) Name() string {
	return appNameNTP
}

func (NTP) Configure(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (NTP) PreStart(ctx context.Context, exp *types.Experiment) error {
	ntpDir := exp.Spec.BaseDir() + "/ntp"
	servers := exp.Spec.Topology().FindNodesWithLabels("ntp-server")

	// If an ntp-server was specified via a node label, use the legacy node-label
	// approach for backwards compatibility with existing topology configurations.
	if len(servers) > 0 {
		return NTP{}.preStartWithNodeLabels(ctx, exp, ntpDir)
	}

	return NTP{}.preStartWithAppConfig(ctx, exp, ntpDir)
}

func (NTP) preStartWithAppConfig(_ context.Context, exp *types.Experiment, ntpDir string) error {
	ntpClientTypes := map[string]ntpTypeConfig{
		"ntp":     {"ntp_linux.tmpl", "/etc/ntp.conf", ""},
		"chrony":  {"chrony_linux.tmpl", "/etc/chrony/chrony.conf", ""},
		"systemd": {"systemd-timesyncd.tmpl", "/etc/systemd/timesyncd.conf", ""},
		"windows": {"ntp_windows.tmpl", "/phenix/startup/25-ntp.ps1", "0755"},
	}

	ntpServerTypes := map[string]ntpTypeConfig{
		"ntpd": {"ntp_linux.tmpl", "/etc/ntp.conf", ""},
		// TODO: Using file location /etc/chrony/chrony.conf default on Debian and Ubuntu. Other distros may use /etc/chrony.conf
		"chronyd": {"chrony_linux.tmpl", "/etc/chrony/chrony.conf", ""},
	}

	for _, app := range exp.Apps() {
		if app.Name() != "ntp" {
			continue
		}

		var amd NTPAppMetadata

		_ = mapstructure.Decode(app.Metadata(), &amd)

		// Might be an empty string, but that's okay... for now.
		defaultSource := amd.DefaultSource.IPAddress(exp)

		for _, host := range app.Hosts() {
			node := exp.Spec.Topology().FindNodeByName(host.Hostname())
			if node == nil {
				continue
			}

			var hmd NTPAppHostMetadata

			_ = mapstructure.Decode(host.Metadata(), &hmd)

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

				tc, ok := ntpClientTypes[strings.ToLower(hmd.Client)]
				if !ok {
					return fmt.Errorf("unknown NTP client type %s provided for host %s", hmd.Client, host.Hostname())
				}

				data := NTPTemplateData{Source: source, Server: false}
				if err := tmpl.CreateFileFromTemplate(tc.tmpl, data, cfg); err != nil {
					return fmt.Errorf("generating NTP client config for host %s: %w", host.Hostname(), err)
				}
				node.AddInject(cfg, tc.dest, tc.mode, "")

				continue
			}

			// It's okay if `source` is an empty string here. If it is, the
			// template will generate a config for the NTP server that prefers
			// the host's clock as the source.
			if hmd.Server != "" {
				tc, ok := ntpServerTypes[strings.ToLower(hmd.Server)]
				if !ok {
					return fmt.Errorf(
						"unknown NTP server type %s provided for host %s",
						hmd.Server,
						host.Hostname(),
					)
				}

				data := NTPTemplateData{Source: source, Server: true}
				if err := tmpl.CreateFileFromTemplate(tc.tmpl, data, cfg); err != nil {
					return fmt.Errorf(
						"generating NTP server config for host %s: %w",
						host.Hostname(),
						err,
					)
				}
				node.AddInject(cfg, tc.dest, tc.mode, "")

				continue
			}

			return fmt.Errorf("host %s missing NTP client/server type", host.Hostname())
		}
	}

	return nil
}

func (NTP) preStartWithNodeLabels(_ context.Context, exp *types.Experiment, ntpDir string) error {
	var (
		servers = exp.Spec.Topology().FindNodesWithLabels("ntp-server")
		server  = servers[0] // use first server if more than one present
	)

	ifaceName := server.Labels()["ntp-server"]
	serverAddr := server.Network().InterfaceAddress(ifaceName)

	if serverAddr == "" {
		return errors.New("no IP address provided for NTP server")
	}

	if err := os.MkdirAll(ntpDir, 0o750); err != nil {
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
			data := NTPTemplateData{Source: serverAddr, Server: false}
			err := tmpl.CreateFileFromTemplate("ntp_linux.tmpl", data, ntpFile)
			if err != nil {
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
		case osLinux, "rhel", "centos":
			data := NTPTemplateData{Source: serverAddr, Server: false}
			err := tmpl.CreateFileFromTemplate("ntp_linux.tmpl", data, ntpFile)
			if err != nil {
				return fmt.Errorf("generating Linux NTP script: %w", err)
			}

			node.AddInject(ntpFile, "/etc/ntp.conf", "", "")
		case osWindows:
			data := NTPTemplateData{Source: serverAddr, Server: false}
			err := tmpl.CreateFileFromTemplate("ntp_windows.tmpl", data, ntpFile)
			if err != nil {
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
