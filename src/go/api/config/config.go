package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"phenix/store"
	"phenix/types"
	"phenix/types/version"
	"phenix/util"
	"phenix/util/common"
	"phenix/util/editor"

	"github.com/hashicorp/go-multierror"
	"gopkg.in/yaml.v3"
)

var AllKinds = []string{"Topology", "Scenario", "Experiment", "Image", "User", "Role"}

var NameRegex = regexp.MustCompile(`^[a-zA-Z0-9_@.-]*$`)

// ConfigHook is a function to be called during the different lifecycle stages
// of a config. The passed config can be updated by the hook functions as
// necessary, and an error can be returned if the lifecycle stage should be
// halted.
type ConfigHook func(string, *store.Config) error

var hooks = make(map[string][]ConfigHook)

// RegisterConfigHook registers a ConfigHook for the given config kind.
func RegisterConfigHook(kind string, hook ConfigHook) {
	hooks[kind] = append(hooks[kind], hook)
}

func init() {
	for _, kind := range AllKinds {
		RegisterConfigHook(kind, func(stage string, c *store.Config) error {
			if stage == "create" || stage == "update" {
				if !NameRegex.MatchString(c.Metadata.Name) {
					return fmt.Errorf("config name is not a valid format")
				}
			}

			return nil
		})
	}
}

func Init() error {
	// Ensure all built-in, default configs are present in the store.
	for _, file := range AssetNames() {
		var c store.Config

		if err := yaml.Unmarshal(MustAsset(file), &c); err != nil {
			return fmt.Errorf("unmarshaling default config %s: %w", file, err)
		}

		name := strings.ToLower(c.Kind) + "/" + c.Metadata.Name

		// Don't attempt to create this default config again if it already exists in
		// the store.
		if _, err := Get(name, false); err == nil {
			continue
		}

		opts := []CreateOption{CreateFromYAML(MustAsset(file))}

		if _, err := Create(opts...); err != nil {
			return fmt.Errorf("storing default config %s: %w", file, err)
		}
	}

	// Recursively traverse default configs directory (e.g. /phenix/configs) and
	// ensure any valid phenix configs are present in the store.
	base := common.PhenixBase + "/configs"

	step := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			var pathErr *fs.PathError
			if errors.As(err, &pathErr) {
				if strings.Contains(err.Error(), fmt.Sprintf("%s: no such file or directory", base)) {
					// Means the base path doesn't exist (which is OK)
					return nil
				}
			}

			return fmt.Errorf("path error: %w", err)
		}

		if d.IsDir() {
			return nil
		}

		var c store.Config

		switch filepath.Ext(path) {
		case ".yaml", ".yml":
			body, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}

			if err := yaml.Unmarshal(body, &c); err != nil {
				return fmt.Errorf("parsing %s: %w", path, err)
			}
		case ".json":
			body, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}

			if err := json.Unmarshal(body, &c); err != nil {
				return fmt.Errorf("parsing %s: %w", path, err)
			}
		default:
			return nil
		}

		name := strings.ToLower(c.Kind) + "/" + c.Metadata.Name

		// `name` will be `/` if the YAML/JSON file parsed was not a valid phenix
		// config (which is OK).
		if name == "/" {
			return nil
		}

		// Don't attempt to create this default config again if it already exists in
		// the store.
		if _, err := Get(name, false); err == nil {
			return nil
		}

		opts := []CreateOption{CreateFromPath(path)}

		// Not checking error here since if there is one it's likely due to the
		// current path not being a valid phenix config (which is OK).
		Create(opts...)

		return nil
	}

	if err := filepath.WalkDir(base, step); err != nil {
		return fmt.Errorf("parsing config files in %s: %w", base, err)
	}

	return nil
}

// List collects configs of the given type (topology, scenario, experiment). If
// no config type is specified, or `all` is specified, then all the known
// configs will be collected. It returns a slice of configs and any errors
// encountered while getting the configs from the store.
func List(which string) (store.Configs, error) {
	var (
		configs store.Configs
		err     error
	)

	switch strings.ToLower(which) {
	case "", "all":
		configs, err = store.List(AllKinds...)
	case "topology":
		configs, err = store.List("Topology")
	case "scenario":
		configs, err = store.List("Scenario")
	case "experiment":
		configs, err = store.List("Experiment")
	case "image":
		configs, err = store.List("Image")
	case "user":
		configs, err = store.List("User")
	case "role":
		configs, err = store.List("Role")
	default:
		return nil, util.HumanizeError(fmt.Errorf("unknown config kind provided: %s", which), "")
	}

	if err != nil {
		return nil, fmt.Errorf("getting list of configs from store: %w", err)
	}

	return configs, nil
}

// Get retrieves the config with the given name. The given name should be of the
// form `type/name`, where `type` is one of `topology, scenario, or experiment`.
// It returns a pointer to the config and any errors encountered while getting
// the config from the store. Note that the returned config will **not** have
// its `spec` and `status` fields casted to the given type, but instead will be
// generic `map[string]interface{}` fields. It's up to the caller to convert
// these fields into the appropriate types.
func Get(name string, upgrade bool) (*store.Config, error) {
	if name == "" {
		return nil, util.HumanizeError(fmt.Errorf("no config name provided"), "")
	}

	c, err := store.NewConfig(name)
	if err != nil {
		return nil, err
	}

	if err := store.Get(c); err != nil {
		return nil, fmt.Errorf("getting config from store: %w", err)
	}

	if upgrade {
		latest := version.StoredVersion[c.Kind]

		if c.APIVersion() != latest {
			upgrader := types.GetUpgrader(c.Kind + "/" + latest)
			if upgrader != nil {
				iface, err := upgrader.Upgrade(c.APIVersion(), c.Spec, c.Metadata)
				if err != nil {
					return nil, fmt.Errorf("upgrading config: %w", err)
				}

				cfg, err := types.NewConfigFromSpec(c.Metadata.Name, iface)
				if err != nil {
					return nil, fmt.Errorf("creating new config from spec: %w", err)
				}

				return cfg, nil
			}
		}
	}

	return c, nil
}

// Create reads a config file from the given path, validates it, and persists it
// to the store. Validation of configs is done against OpenAPIv3 schema
// definitions. In the event the config file being read defines an experiment,
// additional validations are done to ensure the annotated topology (required)
// and scenario (optional) exist. It returns a pointer to the resulting config
// struct and eny errors encountered while creating the config.
func Create(opts ...CreateOption) (*store.Config, error) {
	o := newCreateOptions(opts...)

	var c *store.Config

	if o.config != nil {
		c = o.config
	} else if o.path != "" {
		var err error

		c, err = store.NewConfigFromFile(o.path)
		if err != nil {
			return nil, fmt.Errorf("creating new config from file: %w", err)
		}
	} else if o.data != nil {
		var err error

		data := string(o.data)

		for _, v := range o.scopeVariables {
			data = strings.ReplaceAll(data, v, o.scope)
		}

		switch o.dataType {
		case DataTypeJSON:
			c, err = store.NewConfigFromJSON([]byte(data))
			if err != nil {
				return nil, fmt.Errorf("creating new config from JSON: %w", err)
			}
		case DataTypeYAML:
			c, err = store.NewConfigFromYAML([]byte(data))
			if err != nil {
				return nil, fmt.Errorf("creating new config from YAML: %w", err)
			}
		default:
			return nil, fmt.Errorf("unknown data type provided")
		}
	} else {
		return nil, fmt.Errorf("no config, path, or data provided")
	}

	if o.validate {
		if err := types.ValidateConfigSpec(*c); err != nil {
			return nil, fmt.Errorf("validating config: %w", err)
		}
	}

	for _, hook := range hooks[c.Kind] {
		if err := hook("create", c); err != nil {
			return nil, fmt.Errorf("calling config hook: %w", err)
		}

		if o.validate {
			// Validate again since config hooks can modify the config.
			if err := types.ValidateConfigSpec(*c); err != nil {
				return nil, fmt.Errorf("validating config after config hooks: %w", err)
			}
		}
	}

	if err := store.Create(c); err != nil {
		return nil, fmt.Errorf("storing config: %w", err)
	}

	return c, nil
}

// Edit retrieves the config with the given name for editing. The given name
// should be of the form `type/name`, where `type` is one of `topology,
// scenario, or experiment`. A YAML representation of the config is written to a
// temporary file, and that file is opened for editing using the default editor
// (as defined by the user's `EDITOR` env variable). If no default editor is
// found, `vim` is used. If no changes were made to the file, an error of type
// `editor.ErrNoChange` is returned. This can be checked using the
// `IsConfigNotModified` function. It returns the updated config and any errors
// encountered while editing the config.
func Edit(name string, force bool) (*store.Config, error) {
	if name == "" {
		return nil, fmt.Errorf("no config name provided")
	}

	c, err := store.NewConfig(name)
	if err != nil {
		return nil, err
	}

	if err := store.Get(c); err != nil {
		return nil, fmt.Errorf("getting config from store: %w", err)
	}

	var expName string

	if c.Kind == "Experiment" {
		exp, err := types.DecodeExperimentFromConfig(*c)
		if err != nil {
			return nil, fmt.Errorf("decoding experiment from config: %w", err)
		}

		if !force && exp.Running() {
			return nil, fmt.Errorf("cannot edit running experiment")
		}

		expName = exp.Spec.ExperimentName()
		delete(c.Spec, "experimentName")
	}

	body, err := yaml.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshaling config to YAML: %w", err)
	}

	body, err = editor.EditData(body)
	if err != nil {
		return nil, fmt.Errorf("editing config: %w", err)
	}

	if err := yaml.Unmarshal(body, c); err != nil {
		return nil, fmt.Errorf("unmarshaling config as YAML: %w", err)
	}

	if c.Kind == "Experiment" {
		c.Spec["experimentName"] = expName
	}

	if err := Update(name, c); err != nil {
		return nil, fmt.Errorf("updating edited config: %w", err)
	}

	return c, nil
}

// Update updates the store with the given config. If the name of the config was
// changed as part of the update, a new config will be created and the old
// config deleted.
func Update(name string, c *store.Config) error {
	old, err := store.NewConfig(name)
	if err != nil {
		return fmt.Errorf("getting config to update: %w", err)
	}

	if err := store.Get(old); err != nil {
		return fmt.Errorf("getting config to update: %w", err)
	}

	c.Metadata.Created = old.Metadata.Created

	if err := types.ValidateConfigSpec(*c); err != nil {
		return fmt.Errorf("validating config: %w", err)
	}

	for _, hook := range hooks[c.Kind] {
		if err := hook("update", c); err != nil {
			return fmt.Errorf("calling config hook: %w", err)
		}

		// Validate again since config hooks can modify the config.
		if err := types.ValidateConfigSpec(*c); err != nil {
			return fmt.Errorf("validating config after config hooks: %w", err)
		}
	}

	if err := store.Update(c); err != nil {
		if errors.Is(err, store.ErrNotExist) { // name changed during update
			if err := store.Create(c); err != nil {
				return fmt.Errorf("renaming updated config in store: %w", err)
			}

			if err := store.Delete(old); err != nil {
				store.Delete(c) // don't offer a path to creation via updates
				return fmt.Errorf("renaming updated config in store: %w", err)
			}

			return nil
		}

		return fmt.Errorf("updating config in store: %w", err)
	}

	return nil
}

// Delete removes the config with the given name from the store. The given name
// should be of the form `type/name`, where `type` is one of `topology,
// scenario, or experiment`. If `all` is specified, then all the known configs
// are removed. It returns any errors encountered while removing the config from
// the store.
func Delete(name string) error {
	if name == "" {
		return fmt.Errorf("no config name provided")
	}

	var errors error

	if name == "all" {
		configs, _ := List("all")

		for _, c := range configs {
			if err := store.Delete(&c); err != nil {
				errors = multierror.Append(errors, fmt.Errorf("deleting config %s/%s: %w", c.Kind, c.Metadata.Name, err))
				continue
			}

			for _, hook := range hooks[c.Kind] {
				if err := hook("delete", &c); err != nil {
					errors = multierror.Append(errors, fmt.Errorf("executing delete experiment hook for config %s/%s: %w", c.Kind, c.Metadata.Name, err))
				}
			}
		}

		return errors
	}

	c, err := Get(name, false)
	if err != nil {
		return fmt.Errorf("getting config %s: %w", name, err)
	}

	if err := store.Delete(c); err != nil {
		return fmt.Errorf("deleting config %s: %w", name, err)
	}

	for _, hook := range hooks[c.Kind] {
		if err := hook("delete", c); err != nil {
			errors = multierror.Append(errors, fmt.Errorf("executing delete experiment hook for config %s: %w", name, err))
		}
	}

	return errors
}

// IsConfigNotModified returns a boolean indicating whether the error is known
// to report that a config was not modified during editing. It is satisfied by
// editor.ErrNoChange.
func IsConfigNotModified(err error) bool {
	return errors.Is(err, editor.ErrNoChange)
}
