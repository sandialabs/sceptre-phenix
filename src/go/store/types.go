package store

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"phenix/types/version"

	"gopkg.in/yaml.v3"
)

const API_GROUP = "phenix.sandia.gov"

var ErrInvalidFormat = fmt.Errorf("invalid formatting")

type (
	Configs     []Config
	Annotations map[string]string
)

type Config struct {
	Version  string         `json:"apiVersion" yaml:"apiVersion"`
	Kind     string         `json:"kind" yaml:"kind"`
	Metadata ConfigMetadata `json:"metadata" yaml:"metadata"`
	Spec     map[string]any `json:"spec,omitempty" yaml:"spec,omitempty"`
	Status   map[string]any `json:"status,omitempty" yaml:"status,omitempty"`
}

type ConfigMetadata struct {
	Name        string      `json:"name" yaml:"name"`
	Created     string      `json:"created" yaml:"created"`
	Updated     string      `json:"updated" yaml:"updated"`
	Annotations Annotations `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

func NewConfig(name string) (*Config, error) {
	n := strings.Split(name, "/")

	if len(n) != 2 {
		return nil, fmt.Errorf("invalid config name provided: %s", name)
	}

	kind, name := n[0], n[1]
	kind = strings.Title(kind)

	version := version.StoredVersion[kind]
	version = API_GROUP + "/" + version

	c := Config{
		Version: version,
		Kind:    kind,
		Metadata: ConfigMetadata{
			Name: name,
		},
	}

	return &c, nil
}

func NewConfigFromFile(path string) (*Config, error) {
	file, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config: %w", err)
	}

	var c Config

	switch filepath.Ext(path) {
	case ".json":
		if err := json.Unmarshal(file, &c); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidFormat, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(file, &c); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidFormat, err)
		}
	default:
		return nil, fmt.Errorf("invalid config extension")
	}

	// ensure users aren't trying to set these values
	c.Metadata.Created = ""
	c.Metadata.Updated = ""

	return &c, nil
}

func NewConfigFromJSON(body []byte, replacements ...string) (*Config, error) {
	data := string(body)

	// Starting at 1 handles the case where replacements has an odd number of
	// entries.
	for i := 1; i < len(replacements); i += 2 {
		var (
			tmpl = replacements[i-1]
			val  = replacements[i]
		)

		data = strings.ReplaceAll(data, tmpl, val)
	}

	var c Config

	if err := json.Unmarshal([]byte(data), &c); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFormat, err)
	}

	// ensure users aren't trying to set these values
	c.Metadata.Created = ""
	c.Metadata.Updated = ""

	return &c, nil
}

func NewConfigFromYAML(body []byte, replacements ...string) (*Config, error) {
	data := string(body)

	// Starting at 1 handles the case where replacements has an odd number of
	// entries.
	for i := 1; i < len(replacements); i += 2 {
		var (
			tmpl = replacements[i-1]
			val  = replacements[i]
		)

		data = strings.ReplaceAll(data, tmpl, val)
	}

	var c Config

	if err := yaml.Unmarshal([]byte(data), &c); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFormat, err)
	}

	// ensure users aren't trying to set these values
	c.Metadata.Created = ""
	c.Metadata.Updated = ""

	return &c, nil
}

func (this Config) APIGroup() string {
	s := strings.Split(this.Version, "/")

	if len(s) < 2 {
		return ""
	}

	return s[0]
}

func (this Config) APIVersion() string {
	s := strings.Split(this.Version, "/")

	if len(s) == 0 {
		return ""
	} else if len(s) == 1 {
		return s[0]
	} else {
		return s[1]
	}
}

func (this Config) HasAnnotation(name string) bool {
	if this.Metadata.Annotations == nil {
		return false
	}

	_, ok := this.Metadata.Annotations[name]
	return ok
}

func (this Config) FullName() string {
	return this.Kind + "/" + this.Metadata.Name
}

func ConfigFullName(name ...string) string {
	if len(name) == 1 {
		n := strings.Split(name[0], "/")

		if len(n) != 2 {
			return ""
		}

		return strings.Title(n[0]) + "/" + n[1]
	} else if len(name) == 2 {
		return strings.Title(name[0]) + "/" + name[1]
	}

	return ""
}
