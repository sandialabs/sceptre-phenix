package common

import (
	"fmt"
	"os"
	"regexp"
	"strings"
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

	BridgeMode = BridgeModeManual     //nolint:gochecknoglobals // global config
	DeployMode = DeployModeNoHeadnode //nolint:gochecknoglobals // global config

	UnixSocket = "/tmp/phenix.sock" //nolint:gochecknoglobals // global config

	StoreEndpoint    string //nolint:gochecknoglobals // global config
	HostnameSuffixes string //nolint:gochecknoglobals // global config

	UseGREMesh bool //nolint:gochecknoglobals // global config
)

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
	re := regexp.MustCompile(`\$\{(\w+)(?::([^}]*))?\}`)

	return re.ReplaceAllStringFunc(input, func(match string) string {
		parts := re.FindStringSubmatch(match)
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
