package types

import (
	"phenix/store"
	v2 "phenix/types/version/v2"
)

type Setting struct {
	Metadata store.ConfigMetadata `json:"metadata" yaml:"metadata"`
	Spec     *v2.Setting          `json:"spec" yaml:"spec"`
}
