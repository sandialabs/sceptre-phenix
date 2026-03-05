package v1

import (
	"strings"
)

type Format string

const (
	FormatRaw   Format = "raw"
	FormatQcow2 Format = "qcow2"
	FormatVmdk  Format = "vmdk"
	FormatVdi   Format = "vdi"
	FormatVhdx  Format = "vhdx"
)

type Image struct {
	Name                string            `mapstructure:"name"`
	Variant             string            `mapstructure:"variant"               json:"variant"               yaml:"variant"`
	Release             string            `mapstructure:"release"               json:"release"               yaml:"release"`
	Format              Format            `mapstructure:"format"                json:"format"                yaml:"format"`
	Ramdisk             bool              `mapstructure:"ramdisk"               json:"ramdisk"               yaml:"ramdisk"`
	Compress            bool              `mapstructure:"compress"              json:"compress"              yaml:"compress"`
	Size                string            `mapstructure:"size"                  json:"size"                  yaml:"size"`
	Mirror              string            `mapstructure:"mirror"                json:"mirror"                yaml:"mirror"`
	SkipDefaultPackages bool              `mapstructure:"skip_default_packages" json:"skip_default_packages" yaml:"skip_default_packages"`
	Packages            []string          `mapstructure:"packages"              json:"packages"              yaml:"packages"`
	Overlays            []string          `mapstructure:"overlays"              json:"overlays"              yaml:"overlays"`
	Scripts             map[string]string `mapstructure:"scripts"               json:"scripts"               yaml:"scripts"`
	ScriptOrder         []string          `mapstructure:"script_order"          json:"script_order"          yaml:"script_order"          structs:"script_order"`
	Components          []string          `mapstructure:"components"            json:"components"            yaml:"components"`
	NoVirtuals          bool              `mapstructure:"no_virtuals"           json:"no_virtuals"           yaml:"no_virtuals"           structs:"no_virtuals"`
	Kernel              []string          `mapstructure:"kernel"                json:"kernel"                yaml:"kernel"`

	Cache       bool     `json:"-" mapstructure:"-" structs:"-" yaml:"-"`
	ScriptPaths []string `json:"-" mapstructure:"-" structs:"-" yaml:"-"`
}

func (i Image) PostBuild() string {
	var post []string

	for _, o := range i.ScriptOrder {
		s := i.Scripts[o]

		for l := range strings.SplitSeq(s, "\n") {
			if l == "" {
				continue
			}

			// Add 6 spaces to script lines so YAML is formatted correctly in vmdb file.
			post = append(post, "      "+l)
		}
	}

	return strings.Join(post, "\n")
}
