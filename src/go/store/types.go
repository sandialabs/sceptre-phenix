package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

// Performs case-insensitive lookup of a config kind.
func canonicalKind(kind string) string {
	for k := range version.StoredVersion {
		if strings.EqualFold(k, kind) {
			return k
		}
	}

	return ""
}

func NewConfig(name string) (*Config, error) {
	n := strings.Split(name, "/")

	if len(n) != configNameParts {
		return nil, fmt.Errorf("invalid config name provided: %s", name)
	}

	kind, name := canonicalKind(n[0]), n[1]
	if kind == "" {
		return nil, fmt.Errorf("invalid config kind provided: %s", n[0])
	}

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

// exactYAMLConfig is a Config as [Config.MarshalYAML] writes it.
type exactYAMLConfig struct {
	Version  string            `yaml:"apiVersion"`
	Kind     string            `yaml:"kind"`
	Metadata exactYAMLMetadata `yaml:"metadata"`
	Spec     any               `yaml:"spec,omitempty"`
	Status   any               `yaml:"status,omitempty"`
}

type exactYAMLMetadata struct {
	Name        string `yaml:"name"`
	Created     string `yaml:"created"`
	Updated     string `yaml:"updated"`
	Annotations any    `yaml:"annotations,omitempty"`
}

// MarshalYAML implements [yaml.Marshaler]. The config is written as its
// fields are, except that its annotations, spec and status are as
// [ExactYAML] makes them, so that the YAML loads as the config.
func (c Config) MarshalYAML() (any, error) {
	return exactYAMLConfig{
		Version: c.Version,
		Kind:    c.Kind,
		Metadata: exactYAMLMetadata{
			Name:        c.Metadata.Name,
			Created:     c.Metadata.Created,
			Updated:     c.Metadata.Updated,
			Annotations: exactYAMLUnlessEmpty(c.Metadata.Annotations),
		},
		Spec:   exactYAMLUnlessEmpty(c.Spec),
		Status: exactYAMLUnlessEmpty(c.Status),
	}, nil
}

// exactYAMLUnlessEmpty is m as [ExactYAML] makes it, or nil for an empty m,
// which omitempty leaves out as it leaves out an empty map.
func exactYAMLUnlessEmpty[M ~map[string]V, V any](m M) any {
	if len(m) == 0 {
		return nil
	}

	return ExactYAML(m)
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

		kind := canonicalKind(n[0])
		if kind == "" {
			return ""
		}

		return kind + "/" + n[1]
	} else if len(name) == configNameParts {
		kind := canonicalKind(name[0])
		if kind == "" {
			return ""
		}

		return kind + "/" + name[1]
	}

	return ""
}

// yamlQuoted is a string yaml.v3 writes double quoted, which it reads back
// as it is, whatever the string holds.
type yamlQuoted string

// MarshalYAML implements [yaml.Marshaler].
func (s yamlQuoted) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Tag: "!!str", Value: string(s)}, nil
}

// ExactYAML is value with each string yaml.v3 would not read back as it
// writes it (see [yamlKeeps]) double quoted, in maps and slices at any depth
// and in map keys, so that the YAML of the value loads as the value. The
// maps and slices are copies: a map with string keys becomes a map[any]any,
// whose keys yaml.v3 sorts as it sorts the map's, and a slice an []any, so
// that the YAML is otherwise what yaml.Marshal writes for value.
func ExactYAML(value any) any {
	if text, ok := value.(string); ok {
		if yamlKeeps(text) {
			return text
		}

		return yamlQuoted(text)
	}

	reflected := reflect.ValueOf(value)

	switch reflected.Kind() { //nolint:exhaustive // other kinds hold no strings
	case reflect.Slice:
		// yaml.v3 writes a []byte as a !!binary scalar.
		if reflected.Type().Elem().Kind() == reflect.Uint8 {
			return value
		}

		items := make([]any, reflected.Len())
		for index := range items {
			items[index] = ExactYAML(reflected.Index(index).Interface())
		}

		return items
	case reflect.Map:
		if reflected.Type().Key().Kind() != reflect.String {
			return value
		}

		entries := make(map[any]any, reflected.Len())
		for iter := reflected.MapRange(); iter.Next(); {
			entries[ExactYAML(iter.Key().Interface())] = ExactYAML(iter.Value().Interface())
		}

		return entries
	default:
		return value
	}
}

// yamlKeeps reports whether yaml.v3 reads text back as it writes it. It
// writes a string without a line break as a plain or quoted scalar, which it
// reads back. It writes one with a line break as a literal block scalar,
// which it may not: it drops a leading line break, so "\n" reads back as ""
// and "\n a" as " a", and indents a first line that starts with a tab so
// that the document does not load.
func yamlKeeps(text string) bool {
	if !strings.Contains(text, "\n") {
		return true
	}

	data, err := yaml.Marshal(map[string]string{"v": text})
	if err != nil {
		return false
	}

	var back map[string]string

	return yaml.Unmarshal(data, &back) == nil && back["v"] == text
}
