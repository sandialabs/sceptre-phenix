package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/mapstructure"

	"phenix/tmpl"
	"phenix/types"
	"phenix/util/mm"

	ifaces "phenix/types/interfaces"
)

const appNameSerial = "serial"

var (
	idFormat  = "%s_serial_%s_%d"
	lfFormat  = "/tmp/%s_serial_%s_%s_%d.log"
	optFormat = "-chardev socket,id=%[1]s,path=/tmp/%[1]s,server,nowait -device pci-serial,chardev=%[1]s"

	defaultStartPort = 40500
)

const serialDeviceAnnotation = "phenix/serial-device"

var supportedExternalSerialBaudRates = map[int]struct{}{
	50:      {},
	75:      {},
	110:     {},
	134:     {},
	150:     {},
	200:     {},
	300:     {},
	600:     {},
	1200:    {},
	1800:    {},
	2400:    {},
	4800:    {},
	9600:    {},
	19200:   {},
	38400:   {},
	57600:   {},
	115200:  {},
	230400:  {},
	460800:  {},
	500000:  {},
	576000:  {},
	921600:  {},
	1000000: {},
	1152000: {},
	1500000: {},
	2000000: {},
	2500000: {},
	3000000: {},
	3500000: {},
	4000000: {},
}

type SerialConfig struct {
	Connections []SerialConnectionConfig `mapstructure:"connections"`
}

type SerialConnectionConfig struct {
	Src  string `mapstructure:"src"`
	Dst  string `mapstructure:"dst"`
	Port int    `mapstructure:"port"`
}

type externalSerialConfig struct {
	Host     string `mapstructure:"host"`
	Device   string `mapstructure:"device"`
	BaudRate int    `mapstructure:"baud_rate"`
}

type serialSocatCommand struct {
	host    string
	command string
}

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
	if err := validateSerialDeviceAnnotationScope(exp); err != nil {
		return err
	}

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

	// Check to see if a scenario exists for this experiment and if it contains a
	// "serial" app. If so, configure serial ports according to the app config.
	for _, app := range exp.Apps() {
		if app.Name() == appNameSerial {
			var config SerialConfig

			if err := app.ParseMetadata(&config); err != nil {
				return fmt.Errorf("parsing serial app metadata: %w", err)
			}

			// Track VM-to-external host assignments across all connections so one
			// VM cannot be pinned to physical serial devices on different hosts.
			vmExternalHosts := make(map[string]string)

			for i, conn := range config.Connections {
				if err := serialPreStartConnection(exp, conn, i, vmExternalHosts); err != nil {
					return fmt.Errorf("configuring serial connection %d (%s -> %s): %w", i, conn.Src, conn.Dst, err)
				}
			}
		}
	}

	return nil
}

func (Serial) PostStart(ctx context.Context, exp *types.Experiment) error {
	if err := validateSerialDeviceAnnotationScope(exp); err != nil {
		return err
	}

	// Check to see if a scenario exists for this experiment and if it contains a
	// "serial" app. If so, configure serial ports according to the app config.
	for _, app := range exp.Apps() {
		if app.Name() == appNameSerial {
			var config SerialConfig

			if err := app.ParseMetadata(&config); err != nil {
				return fmt.Errorf("parsing serial app metadata: %w", err)
			}

			for i, conn := range config.Connections {
				commands, err := serialPostStartCommands(exp, conn, i)
				if err != nil {
					return fmt.Errorf("configuring serial connection %d (%s -> %s): %w", i, conn.Src, conn.Dst, err)
				}

				for _, command := range commands {
					if err := mm.MeshBackground(command.host, command.command); err != nil {
						return fmt.Errorf("starting socat on %s: %w", command.host, err)
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

func serialPreStartConnection(exp *types.Experiment, conn SerialConnectionConfig, idx int, vmExternalHosts map[string]string) error {
	src, dst, err := serialConnectionNodes(exp, conn)
	if err != nil {
		return err
	}

	switch {
	case src.External() && dst.External():
		return fmt.Errorf("external-to-external serial links are not supported")
	case src.External() || dst.External():
		vm, external := serialVMExternalNodes(src, dst)

		cfg, err := parseExternalSerialConfig(external)
		if err != nil {
			return err
		}

		if err := scheduleVMForExternalSerial(exp, vm, cfg.Host, vmExternalHosts); err != nil {
			return err
		}

		if err := appendQEMUFlags(exp.Metadata.Name, vm, idx); err != nil {
			return err
		}
	default:
		if err := appendQEMUFlags(exp.Metadata.Name, src, idx); err != nil {
			return err
		}

		if err := appendQEMUFlags(exp.Metadata.Name, dst, idx); err != nil {
			return err
		}
	}

	return nil
}

func serialPostStartCommands(exp *types.Experiment, conn SerialConnectionConfig, idx int) ([]serialSocatCommand, error) {
	src, dst, err := serialConnectionNodes(exp, conn)
	if err != nil {
		return nil, err
	}

	switch {
	case src.External() && dst.External():
		return nil, fmt.Errorf("external-to-external serial links are not supported")
	case src.External() || dst.External():
		vm, external := serialVMExternalNodes(src, dst)

		cfg, err := parseExternalSerialConfig(external)
		if err != nil {
			return nil, err
		}

		return vmToExternalSerialSocatCommand(exp, conn, idx, vm, cfg)
	default:
		return vmToVMSerialSocatCommands(exp.Metadata.Name, conn, idx, exp.Status.Schedules()), nil
	}
}

func serialConnectionNodes(exp *types.Experiment, conn SerialConnectionConfig) (ifaces.NodeSpec, ifaces.NodeSpec, error) {
	if strings.TrimSpace(conn.Src) == "" {
		return nil, nil, fmt.Errorf("source node is required")
	}

	if strings.TrimSpace(conn.Dst) == "" {
		return nil, nil, fmt.Errorf("destination node is required")
	}

	src := exp.Spec.Topology().FindNodeByName(conn.Src)
	if src == nil {
		return nil, nil, fmt.Errorf("source node %q not found", conn.Src)
	}

	dst := exp.Spec.Topology().FindNodeByName(conn.Dst)
	if dst == nil {
		return nil, nil, fmt.Errorf("destination node %q not found", conn.Dst)
	}

	return src, dst, nil
}

func serialVMExternalNodes(src, dst ifaces.NodeSpec) (ifaces.NodeSpec, ifaces.NodeSpec) {
	if src.External() {
		return dst, src
	}

	return src, dst
}

func validateSerialDeviceAnnotationScope(exp *types.Experiment) error {
	for _, node := range exp.Spec.Topology().Nodes() {
		if node.External() {
			continue
		}

		if _, ok := node.GetAnnotation(serialDeviceAnnotation); ok {
			return fmt.Errorf(
				"node %q has %q annotation, which is only valid on external nodes",
				node.General().Hostname(),
				serialDeviceAnnotation,
			)
		}
	}

	return nil
}

func scheduleVMForExternalSerial(exp *types.Experiment, vm ifaces.NodeSpec, externalHost string, vmExternalHosts map[string]string) error {
	vmName := vm.General().Hostname()
	if vmName == "" {
		return fmt.Errorf("VM endpoint hostname is required")
	}

	if host, ok := vmExternalHosts[vmName]; ok && host != externalHost {
		return fmt.Errorf("VM %q connects to external serial nodes on different hosts: %q and %q", vmName, host, externalHost)
	}

	schedules := exp.Spec.Schedules()
	if schedules == nil {
		schedules = make(map[string]string)
	}

	if scheduledHost := schedules[vmName]; scheduledHost != "" && scheduledHost != externalHost {
		return fmt.Errorf(
			"VM %q is scheduled to host %q but external serial device is on host %q",
			vmName,
			scheduledHost,
			externalHost,
		)
	}

	schedules[vmName] = externalHost
	exp.Spec.SetSchedule(schedules)
	vmExternalHosts[vmName] = externalHost

	return nil
}

func parseExternalSerialConfig(node ifaces.NodeSpec) (externalSerialConfig, error) {
	nodeName := node.General().Hostname()

	raw, ok := node.GetAnnotation(serialDeviceAnnotation)
	if !ok {
		return externalSerialConfig{}, fmt.Errorf("external node %q missing %q annotation", nodeName, serialDeviceAnnotation)
	}

	var cfg externalSerialConfig
	if err := mapstructure.Decode(raw, &cfg); err != nil {
		return externalSerialConfig{}, fmt.Errorf("decoding %q annotation for external node %q: %w", serialDeviceAnnotation, nodeName, err)
	}

	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Device = strings.TrimSpace(cfg.Device)

	if err := validateExternalSerialHost(cfg.Host); err != nil {
		return externalSerialConfig{}, fmt.Errorf("invalid %q annotation for external node %q: %w", serialDeviceAnnotation, nodeName, err)
	}

	if err := validateExternalSerialDevice(cfg.Device); err != nil {
		return externalSerialConfig{}, fmt.Errorf("invalid %q annotation for external node %q: %w", serialDeviceAnnotation, nodeName, err)
	}

	if err := validateExternalSerialBaudRate(cfg.BaudRate); err != nil {
		return externalSerialConfig{}, fmt.Errorf("invalid %q annotation for external node %q: %w", serialDeviceAnnotation, nodeName, err)
	}

	return cfg, nil
}

func validateExternalSerialHost(host string) error {
	if host == "" {
		return fmt.Errorf("host is required")
	}

	if strings.ContainsAny(host, " \t\r\n") {
		return fmt.Errorf("host %q cannot contain whitespace", host)
	}

	return nil
}

func validateExternalSerialDevice(device string) error {
	if device == "" {
		return fmt.Errorf("device is required")
	}

	if strings.ContainsAny(device, " \t\r\n") {
		return fmt.Errorf("device %q cannot contain whitespace", device)
	}

	if !strings.HasPrefix(device, "/dev/") {
		return fmt.Errorf("device %q must be an absolute /dev path", device)
	}

	clean := filepath.Clean(device)
	if clean != device || !strings.HasPrefix(clean, "/dev/") {
		return fmt.Errorf("device %q must be a clean absolute /dev path", device)
	}

	if strings.ContainsAny(device, ",;&|`$<>(){}[]*?!'\"\\") {
		return fmt.Errorf("device %q contains unsupported characters", device)
	}

	return nil
}

func validateExternalSerialBaudRate(baudRate int) error {
	if baudRate == 0 {
		return nil
	}

	if baudRate < 0 {
		return fmt.Errorf("baud_rate must be positive")
	}

	if _, ok := supportedExternalSerialBaudRates[baudRate]; !ok {
		return fmt.Errorf("baud_rate %d is not supported", baudRate)
	}

	return nil
}

func vmToVMSerialSocatCommands(expName string, conn SerialConnectionConfig, idx int, schedule map[string]string) []serialSocatCommand {
	var (
		logFile = fmt.Sprintf(lfFormat, expName, conn.Src, conn.Dst, idx)
		srcID   = fmt.Sprintf(idFormat, expName, conn.Src, idx)
		dstID   = fmt.Sprintf(idFormat, expName, conn.Dst, idx)
		srcHost = schedule[conn.Src]
		dstHost = schedule[conn.Dst]
	)

	if srcHost == dstHost { // single socat process on host connecting unix sockets
		socat := fmt.Sprintf("socat -lf%s -d -d -d -d UNIX-CONNECT:/tmp/%s UNIX-CONNECT:/tmp/%s", logFile, srcID, dstID)

		return []serialSocatCommand{{host: srcHost, command: socat}}
	}

	port := conn.Port
	if port == 0 {
		port = defaultStartPort + idx
	}

	srcSocat := fmt.Sprintf("socat -lf%s -d -d -d -d UNIX-CONNECT:/tmp/%s TCP-LISTEN:%d", logFile, srcID, port)
	dstSocat := fmt.Sprintf("socat -lf%s -d -d -d -d UNIX-CONNECT:/tmp/%s TCP-CONNECT:%s:%d", logFile, dstID, srcHost, port)

	return []serialSocatCommand{
		{host: srcHost, command: srcSocat},
		{host: dstHost, command: dstSocat},
	}
}

func vmToExternalSerialSocatCommand(exp *types.Experiment, conn SerialConnectionConfig, idx int, vm ifaces.NodeSpec, cfg externalSerialConfig) ([]serialSocatCommand, error) {
	vmName := vm.General().Hostname()
	vmHost := exp.Status.Schedules()[vmName]
	if vmHost == "" {
		return nil, fmt.Errorf("VM %q has no runtime schedule for external serial host %q", vmName, cfg.Host)
	}

	if vmHost != cfg.Host {
		return nil, fmt.Errorf(
			"VM %q is running on host %q but external serial device is on host %q",
			vmName,
			vmHost,
			cfg.Host,
		)
	}

	logFile := fmt.Sprintf(lfFormat, exp.Metadata.Name, conn.Src, conn.Dst, idx)
	vmID := fmt.Sprintf(idFormat, exp.Metadata.Name, vmName, idx)
	socat := fmt.Sprintf("socat -lf%s -d -d -d -d UNIX-CONNECT:/tmp/%s %s", logFile, vmID, cfg.socatAddress())

	return []serialSocatCommand{{host: cfg.Host, command: socat}}, nil
}

func (cfg externalSerialConfig) socatAddress() string {
	address := fmt.Sprintf("OPEN:%s,raw,echo=0", cfg.Device)
	if cfg.BaudRate != 0 {
		address += fmt.Sprintf(",b%d", cfg.BaudRate)
	}

	return address
}

func appendQEMUFlags(exp string, node ifaces.NodeSpec, idx int) error {
	var (
		id      = fmt.Sprintf(idFormat, exp, node.General().Hostname(), idx)
		options = fmt.Sprintf(optFormat, id)
	)

	var qemuAppend []string

	if advanced := node.Advanced(); advanced != nil {
		if v, ok := advanced["qemu-append"]; ok {
			if strings.Contains(v, options) {
				return nil
			}

			qemuAppend = []string{v}
		}
	}

	qemuAppend = append(qemuAppend, options)
	node.AddAdvanced("qemu-append", strings.Join(qemuAppend, " "))

	return nil
}
