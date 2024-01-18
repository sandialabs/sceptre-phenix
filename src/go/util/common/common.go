package common

import (
	"fmt"
	"strings"
)

type DeploymentMode string

const (
	DEPLOY_MODE_UNSET         DeploymentMode = ""
	DEPLOY_MODE_NO_HEADNODE   DeploymentMode = "no-headnode"
	DEPLOY_MODE_ONLY_HEADNODE DeploymentMode = "only-headnode"
	DEPLOY_MODE_ALL           DeploymentMode = "all"
)

var (
	PhenixBase   = "/phenix"
	MinimegaBase = "/tmp/minimega"

	DeployMode = DEPLOY_MODE_NO_HEADNODE

	LogFile    = "/var/log/phenix/phenix.log"
	ErrorFile  = "/var/log/phenix/error.log"
	UnixSocket = "/tmp/phenix.sock"

	StoreEndpoint    string
	HostnameSuffixes string
)

func TrimHostnameSuffixes(str string) string {
	for _, s := range strings.Split(HostnameSuffixes, ",") {
		str = strings.TrimSuffix(str, s)
	}

	return str
}

func ParseDeployMode(mode string) (DeploymentMode, error) {
	switch strings.ToLower(mode) {
	case "no-headnode":
		return DEPLOY_MODE_NO_HEADNODE, nil
	case "only-headnode":
		return DEPLOY_MODE_ONLY_HEADNODE, nil
	case "all":
		return DEPLOY_MODE_ALL, nil
	}

	return DEPLOY_MODE_UNSET, fmt.Errorf("unknown deploy mode provided: %s", mode)
}
