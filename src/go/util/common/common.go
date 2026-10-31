package common

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
)

type (
	BridgingMode   string
	DeploymentMode string
)

const (
	BridgeModeUnset  BridgingMode = ""
	BridgeModeManual BridgingMode = "manual"
	BridgeModeAuto   BridgingMode = "auto"
)

const (
	DeployModeUnset        DeploymentMode = ""
	DeployModeNoHeadnode   DeploymentMode = "no-headnode"
	DeployModeOnlyHeadnode DeploymentMode = "only-headnode"
	DeployModeAll          DeploymentMode = "all"
)

var (
	PhenixBase   = "/phenix"       //nolint:gochecknoglobals // global config
	MinimegaBase = "/tmp/minimega" //nolint:gochecknoglobals // global config

	// MountBase is the base directory used to store VM filesystem mounts. When
	// empty, callers should default to PhenixBase + "/mounts".
	MountBase string //nolint:gochecknoglobals // global config

	// InjectsBase is the effective base directory for staged workflow
	// injects. The root command sets it from base-dir.injects, or to
	// PhenixBase + "/injects" when that setting is empty.
	InjectsBase string //nolint:gochecknoglobals // global config

	// TopologiesBase is the effective base directory in which phenix workflow
	// apply looks up topology directories by name. The root command sets it
	// from base-dir.topologies, or to PhenixBase + "/topologies" when that
	// setting is empty.
	TopologiesBase string //nolint:gochecknoglobals // global config

	BridgeMode = BridgeModeManual     //nolint:gochecknoglobals // global config
	DeployMode = DeployModeNoHeadnode //nolint:gochecknoglobals // global config

	UnixSocket = "/tmp/phenix.sock" //nolint:gochecknoglobals // global config

	StoreEndpoint    string //nolint:gochecknoglobals // global config
	HostnameSuffixes string //nolint:gochecknoglobals // global config

	UseGREMesh bool //nolint:gochecknoglobals // global config
)

// MountDir returns the effective base directory to use for VM filesystem
// mounts. If MountBase has not been explicitly configured, it defaults to
// PhenixBase + "/mounts".
func MountDir() string {
	if MountBase == "" {
		return filepath.Join(PhenixBase, "mounts")
	}

	return MountBase
}

func TrimHostnameSuffixes(str string) string {
	for s := range strings.SplitSeq(HostnameSuffixes, ",") {
		str = strings.TrimSuffix(str, s)
	}

	return str
}

func ParseBridgeMode(mode string) (BridgingMode, error) {
	switch strings.ToLower(mode) {
	case "manual":
		return BridgeModeManual, nil
	case "auto":
		return BridgeModeAuto, nil
	case "": // default to current setting
		return BridgeMode, nil
	}

	return BridgeModeUnset, fmt.Errorf("unknown bridge mode provided: %s", mode)
}

func SetBridgeMode(mode string) error {
	parsed, err := ParseBridgeMode(mode)
	if err != nil {
		return fmt.Errorf("setting bridge mode: %w", err)
	}

	BridgeMode = parsed

	return nil
}

func ParseDeployMode(mode string) (DeploymentMode, error) {
	switch strings.ToLower(mode) {
	case "no-headnode":
		return DeployModeNoHeadnode, nil
	case "only-headnode":
		return DeployModeOnlyHeadnode, nil
	case "all":
		return DeployModeAll, nil
	case "": // default to current setting
		return DeployMode, nil
	}

	return DeployModeUnset, fmt.Errorf("unknown deploy mode provided: %s", mode)
}

func SetDeployMode(mode string) error {
	parsed, err := ParseDeployMode(mode)
	if err != nil {
		return fmt.Errorf("setting deploy mode: %w", err)
	}

	DeployMode = parsed

	return nil
}

// DiskCheck is what starting an experiment does when a VM's disk image or CD-ROM image is missing, or its backing
// chain is broken: the experiment.disk-check setting.
type DiskCheck string

// The values of the experiment.disk-check setting.
const (
	// DiskCheckError refuses the start; it is the default.
	DiskCheckError DiskCheck = "error"
	// DiskCheckWarn starts anyway and reports each problem as a warning.
	DiskCheckWarn DiskCheck = "warn"
	// DiskCheckOff does not check the images.
	DiskCheckOff DiskCheck = "off"
)

// diskCheck holds the current DiskCheck; the config watcher of phenix ui changes it while starts read it.
var diskCheck atomic.Value //nolint:gochecknoglobals // global config

// ParseDiskCheck returns the DiskCheck mode names, ignoring case; "" is DiskCheckError.
func ParseDiskCheck(mode string) (DiskCheck, error) {
	switch DiskCheck(strings.ToLower(mode)) {
	case DiskCheckError, "":
		return DiskCheckError, nil
	case DiskCheckWarn:
		return DiskCheckWarn, nil
	case DiskCheckOff:
		return DiskCheckOff, nil
	}

	return DiskCheckError, fmt.Errorf("unknown disk check mode provided: %s", mode)
}

// SetDiskCheck sets the current DiskCheck to the mode names, leaving it unchanged when ParseDiskCheck refuses it.
func SetDiskCheck(mode string) error {
	parsed, err := ParseDiskCheck(mode)
	if err != nil {
		return fmt.Errorf("setting disk check mode: %w", err)
	}

	diskCheck.Store(parsed)

	return nil
}

// CurrentDiskCheck returns the current DiskCheck: DiskCheckError until SetDiskCheck sets another.
func CurrentDiskCheck() DiskCheck {
	if mode, ok := diskCheck.Load().(DiskCheck); ok {
		return mode
	}

	return DiskCheckError
}

// EnvPlaceholder matches the ${VAR} and ${VAR:default} placeholders that
// [ParseEnv] replaces: the name in its first group, the default in its second.
var EnvPlaceholder = regexp.MustCompile(`\$\{(\w+)(?::([^}]*))?\}`)

// ParseEnv replaces environment variable placeholders in the input string with their corresponding values.
// Placeholders are in the format ${VAR} or ${VAR:default}, where VAR is the environment variable name,
// and default is an optional default value to use if the variable is not set.
// If the environment variable is not found and no default is provided, the placeholder is replaced with an empty string.
//
// Example:
//
//	os.Setenv("FOO", "bar")
//	ParseEnv("Value: ${FOO}, Default: ${BAZ:qux}") // returns "Value: bar, Default: qux"
func ParseEnv(input string) string {
	return EnvPlaceholder.ReplaceAllStringFunc(input, func(match string) string {
		parts := EnvPlaceholder.FindStringSubmatch(match)
		if len(parts) == 0 {
			return match
		}

		key := parts[1]
		defaultValue := parts[2] // May be empty if no default provided

		if value, found := os.LookupEnv(key); found {
			return value
		}

		return defaultValue // Return default value (empty string if no default was provided)
	})
}
