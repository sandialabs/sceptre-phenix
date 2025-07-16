package v1

import (
	"strings"
)

type Format string

const (
	Format_Raw   Format = "raw"
	Format_Qcow2 Format = "qcow2"
	Format_Vmdk  Format = "vmdk"
	Format_Vdi   Format = "vdi"
	Format_Vhdx  Format = "vhdx"
)

type Image struct {
	Name                string
	Variant             string            `json:"variant" yaml:"variant"`
	Release             string            `json:"release" yaml:"release"`
	Format              Format            `json:"format" yaml:"format"`
	Ramdisk             bool              `json:"ramdisk" yaml:"ramdisk"`
	Compress            bool              `json:"compress" yaml:"compress"`
	Size                string            `json:"size" yaml:"size"`
	Mirror              string            `json:"mirror" yaml:"mirror"`
	SkipDefaultPackages bool              `json:"skip_default_packages" yaml:"skip_default_packages"`
	Packages            []string          `json:"packages" yaml:"packages"`
	Overlays            []string          `json:"overlays" yaml:"overlays"`
	Scripts             map[string]string `json:"scripts" yaml:"scripts"`
	ScriptOrder         []string          `json:"script_order" yaml:"script_order" structs:"script_order" mapstructure:"script_order"`
	Components          []string          `json:"components" yaml:"components"`
	NoVirtuals          bool              `json:"no_virtuals" yaml:"no_virtuals" structs:"no_virtuals" mapstructure:"no_virtuals"`
	Kernel              []string          `json:"kernel" yaml:"kernel"`

	Cache       bool     `json:"-" yaml:"-" structs:"-" mapstructure:"-"`
	ScriptPaths []string `json:"-" yaml:"-" structs:"-" mapstructure:"-"`
}

func (this Image) PostBuild() string {
	var post []string

	for _, o := range this.ScriptOrder {
		s := this.Scripts[o]

		for _, l := range strings.Split(s, "\n") {
			if l == "" {
				continue
			}

			// Add 6 spaces to script lines so YAML is formatted correctly in vmdb file.
			post = append(post, "      "+l)
		}
	}

	return strings.Join(post, "\n")
}
