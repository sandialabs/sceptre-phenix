package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"phenix/types/version"
	"phenix/util/common"
)

const (
	APIGroup        = "phenix.sandia.gov"
	configNameParts = 2
)

var ErrInvalidFormat = errors.New("invalid formatting")

type (
	Configs     []Config
	Annotations map[string]string
)

type Config struct {
	Version  string         `json:"apiVersion"       yaml:"apiVersion"`
	Kind     string         `json:"kind"             yaml:"kind"`
	Metadata ConfigMetadata `json:"metadata"         yaml:"metadata"`
	Spec     map[string]any `json:"spec,omitempty"   yaml:"spec,omitempty"`
	Status   map[string]any `json:"status,omitempty" yaml:"status,omitempty"`
}

type ConfigMetadata struct {
	Name        string      `json:"name"                  yaml:"name"`
	Created     string      `json:"created"               yaml:"created"`
	Updated     string      `json:"updated"               yaml:"updated"`
	Annotations Annotations `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

func NewConfig(name string) (*Config, error) {
	n := strings.Split(name, "/")

	if len(n) != configNameParts {
		return nil, fmt.Errorf("invalid config name provided: %s", name)
	}

	kind, name := n[0], n[1]
	kind = strings.ToUpper(kind[:1]) + kind[1:]

	version := version.StoredVersion[kind]
	version = APIGroup + "/" + version

	c := Config{ //nolint:exhaustruct // partial initialization
		Version: version,
		Kind:    kind,
		Metadata: ConfigMetadata{ //nolint:exhaustruct // partial initialization
			Name: name,
		},
	}

	return &c, nil
}

func NewConfigFromFile(path string) (*Config, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config: %w", err)
	}

	// Parse environment variables in the file
	file = []byte(common.ParseEnv(string(file)))

	var c Config

	switch filepath.Ext(path) {
	case ".json":
		err := json.Unmarshal(file, &c)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidFormat, err)
		}
	case ".yaml", ".yml":
		err := yaml.Unmarshal(file, &c)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidFormat, err)
		}
	default:
		return nil, errors.New("invalid config extension")
	}

	// ensure users aren't trying to set these values
	c.Metadata.Created = ""
	c.Metadata.Updated = ""

	return &c, nil
}

func NewConfigFromJSON(body []byte) (*Config, error) {
	// Parse environment variables in the file
	data := common.ParseEnv(string(body))

	var c Config

	err := json.Unmarshal([]byte(data), &c)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidFormat, err)
	}

	// ensure users aren't trying to set these values
	c.Metadata.Created = ""
	c.Metadata.Updated = ""

	return &c, nil
}

func NewConfigFromYAML(body []byte) (*Config, error) {
	// Parse environment variables in the file
	data := common.ParseEnv(string(body))

	var c Config

	err := yaml.Unmarshal([]byte(data), &c)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidFormat, err)
	}

	// ensure users aren't trying to set these values
	c.Metadata.Created = ""
	c.Metadata.Updated = ""

	return &c, nil
}

func (c Config) APIGroup() string {
	s := strings.Split(c.Version, "/")

	if len(s) < configNameParts {
		return ""
	}

	return s[0]
}

func (c Config) APIVersion() string {
	s := strings.Split(c.Version, "/")

	switch len(s) {
	case 0:
		return ""
	case 1:
		return s[0]
	default:
		return s[1]
	}
}

func (c Config) HasAnnotation(name string) bool {
	if c.Metadata.Annotations == nil {
		return false
	}

	_, ok := c.Metadata.Annotations[name]

	return ok
}

func (c Config) FullName() string {
	return c.Kind + "/" + c.Metadata.Name
}

func ConfigFullName(name ...string) string {
	if len(name) == 1 {
		n := strings.Split(name[0], "/")

		if len(n) != configNameParts {
			return ""
		}

		return strings.ToUpper(n[0][:1]) + n[0][1:] + "/" + n[1]
	} else if len(name) == configNameParts {
		return strings.ToUpper(name[0][:1]) + name[0][1:] + "/" + name[1]
	}

	return ""
}
