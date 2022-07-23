package common

import (
	"strings"
)

var (
	PhenixBase   = "/phenix"
	MinimegaBase = "/tmp/minimega"

	LogFile   = "/var/log/phenix/phenix.log"
	ErrorFile = "/var/log/phenix/error.log"

	StoreEndpoint    string
	HostnameSuffixes string
)

func TrimHostnameSuffixes(str string) string {
	for _, s := range strings.Split(HostnameSuffixes, ",") {
		str = strings.TrimSuffix(str, s)
	}

	return str
}
