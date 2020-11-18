package common

import (
	"strings"
)

var (
	PhenixBase   = "/phenix"
	MinimegaBase = "/tmp/minimega"

	LogFile = "/var/log/phenix"

	HostnameSuffixes string
)

func TrimHostnameSuffixes(str string) string {
	for _, s := range strings.Split(HostnameSuffixes, ",") {
		str = strings.TrimSuffix(str, s)
	}

	return str
}
