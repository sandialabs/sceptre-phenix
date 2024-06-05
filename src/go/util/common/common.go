package common

import (
	"fmt"
	"strings"
)

type (
	BridgingMode   string
	DeploymentMode string
)

const (
	BRIDGE_MODE_UNSET  BridgingMode = ""
	BRIDGE_MODE_MANUAL BridgingMode = "manual"
	BRIDGE_MODE_AUTO   BridgingMode = "auto"
)

const (
	DEPLOY_MODE_UNSET         DeploymentMode = ""
	DEPLOY_MODE_NO_HEADNODE   DeploymentMode = "no-headnode"
	DEPLOY_MODE_ONLY_HEADNODE DeploymentMode = "only-headnode"
	DEPLOY_MODE_ALL           DeploymentMode = "all"
)

var (
	PhenixBase   = "/phenix"
	MinimegaBase = "/tmp/minimega"

	BridgeMode = BRIDGE_MODE_MANUAL
	DeployMode = DEPLOY_MODE_NO_HEADNODE

	LogFile    = "/var/log/phenix/phenix.log"
	ErrorFile  = "/var/log/phenix/error.log"
	UnixSocket = "/tmp/phenix.sock"

	StoreEndpoint    string
	HostnameSuffixes string

	UseGREMesh bool
)

func TrimHostnameSuffixes(str string) string {
	for _, s := range strings.Split(HostnameSuffixes, ",") {
		str = strings.TrimSuffix(str, s)
	}

	return str
}

func ParseBridgeMode(mode string) (BridgingMode, error) {
	switch strings.ToLower(mode) {
	case "manual":
		return BRIDGE_MODE_MANUAL, nil
	case "auto":
		return BRIDGE_MODE_AUTO, nil
	case "": // default to current setting
		return BridgeMode, nil
	}

	return BRIDGE_MODE_UNSET, fmt.Errorf("unknown bridge mode provided: %s", mode)
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
		return DEPLOY_MODE_NO_HEADNODE, nil
	case "only-headnode":
		return DEPLOY_MODE_ONLY_HEADNODE, nil
	case "all":
		return DEPLOY_MODE_ALL, nil
	case "": // default to current setting
		return DeployMode, nil
	}

	return DEPLOY_MODE_UNSET, fmt.Errorf("unknown deploy mode provided: %s", mode)
}

func SetDeployMode(mode string) error {
	parsed, err := ParseDeployMode(mode)
	if err != nil {
		return fmt.Errorf("setting deploy mode: %w", err)
	}

	DeployMode = parsed

	return nil
}
