package config

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hashicorp/go-multierror"
	"gopkg.in/yaml.v3"

	"phenix/store"
	"phenix/types"
	"phenix/types/version"
	"phenix/util"
	"phenix/util/common"
	"phenix/util/editor"
)

//go:embed default
var defaultFS embed.FS

var AllKinds = []string{"Topology", "Scenario", "Experiment", "Image", "User", "Role"} //nolint:gochecknoglobals // global constant

var NameRegex = regexp.MustCompile(`^[a-zA-Z0-9_@.-]*$`)

// ConfigHook is a function to be called during the different lifecycle stages
// of a config. The passed config can be updated by the hook functions as
// necessary, and an error can be returned if the lifecycle stage should be
// halted.
type ConfigHook func(string, *store.Config) error

var hooks = make(map[string][]ConfigHook) //nolint:gochecknoglobals // global hooks

// RegisterConfigHook registers a ConfigHook for the given config kind.
func RegisterConfigHook(kind string, hook ConfigHook) {
	hooks[kind] = append(hooks[kind], hook)
}

func init() { //nolint:gochecknoinits // config hook
	for _, kind := range AllKinds {
		RegisterConfigHook(kind, func(stage string, c *store.Config) error {
			if stage == "create" || stage == "update" {
				if !NameRegex.MatchString(c.Metadata.Name) {
					return errors.New("config name is not a valid format")
				}
			}

			return nil
		})
	}
}

//nolint:cyclop,funlen,gocyclo // complex init logic
func Init() error {
	// Ensure all built-in, default configs are present in the store.
	err := fs.WalkDir(defaultFS, "default", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			return nil
		}

		content, err := defaultFS.ReadFile(path)
		if err != nil {
			return err
		}

		var c store.Config
		err = yaml.Unmarshal(content, &c)
		if err != nil {
			return fmt.Errorf("unmarshaling default config %s: %w", path, err)
		}

		name := strings.ToLower(c.Kind) + "/" + c.Metadata.Name

		// Don't attempt to create this default config again if it already exists in
		// the store.
		if _, err := Get(name, false); err == nil {
			return nil // continue to next file
		}

		opts := []CreateOption{CreateFromYAML(content)}

		if _, err := Create(opts...); err != nil {
			return fmt.Errorf("storing default config %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Recursively traverse default configs directory (e.g. /phenix/configs) and
	// ensure any valid phenix configs are present in the store.
	base := common.PhenixBase + "/configs"

	step := func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			var pathErr *fs.PathError
			if errors.As(walkErr, &pathErr) {
				if strings.Contains(walkErr.Error(), base+": no such file or directory") {
					// Means the base path doesn't exist (which is OK)
					return nil
				}
			}

			return fmt.Errorf("path error: %w", walkErr)
		}

		if d.IsDir() {
			return nil
		}

		var c store.Config

		// Set BRANCH_NAME to parent directory name of config file being processed
		// e.g. /phenix/configs/foo/topology.yaml -> BRANCH_NAME=foo
		_ = os.Setenv("BRANCH_NAME", filepath.Base(filepath.Dir(filepath.Clean(path))))

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
		_, _ = Create(opts...)

		return nil
	}

	if err = filepath.WalkDir(base, step); err != nil {
		return fmt.Errorf("parsing config files in %s: %w", base, err)
	}

	// Call any hooks registered for the `startup` stage.
	configs, errList := store.List(AllKinds...)
	if errList != nil {
		return fmt.Errorf("getting list of configs from store: %w", errList)
	}

	for _, config := range configs {
		var updated bool

		for _, hook := range hooks[config.Kind] {
			errHook := hook("startup", &config)
			if errHook != nil {
				return fmt.Errorf("calling startup config hook: %w", errHook)
			}

			updated = true
		}

		if updated {
			errUpdate := store.Update(&config)
			if errUpdate != nil {
				return fmt.Errorf("updating config in store: %w", errUpdate)
			}
		}
	}

	// Mark configs as initialized in the store.
	if err = store.InitializeComponent(store.ComponentConfigs); err != nil {
		return fmt.Errorf("marking configs as initialized: %w", err)
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
		return nil, util.HumanizeError(errors.New("no config name provided"), "")
	}

	c, err := store.NewConfig(name)
	if err != nil {
		return nil, err
	}

	if err = store.Get(c); err != nil {
		return nil, fmt.Errorf("getting config from store: %w", err)
	}

	if upgrade {
		latest := version.StoredVersion[c.Kind]

		if c.APIVersion() != latest {
			upgrader := types.GetUpgrader(c.Kind + "/" + latest)
			if upgrader != nil {
				var iface any
				iface, err = upgrader.Upgrade(c.APIVersion(), c.Spec, c.Metadata)
				if err != nil {
					return nil, fmt.Errorf("upgrading config: %w", err)
				}

				var cfg *store.Config
				cfg, err = types.NewConfigFromSpec(c.Metadata.Name, iface)
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

	switch {
	case o.config != nil:
		c = o.config
	case o.path != "":
		var err error

		c, err = store.NewConfigFromFile(o.path)
		if err != nil {
			return nil, fmt.Errorf("creating new config from file: %w", err)
		}
	case o.data != nil:
		var err error

		data := string(o.data)

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
		case DataTypeUnknown:
			fallthrough
		default:
			return nil, errors.New("unknown data type provided")
		}
	default:
		return nil, errors.New("no config, path, or data provided")
	}

	if o.validate {
		validateErr := types.ValidateConfigSpec(*c)
		if validateErr != nil {
			return nil, fmt.Errorf("validating config: %w", validateErr)
		}
	}

	for _, hook := range hooks[c.Kind] {
		hookErr := hook("create", c)
		if hookErr != nil {
			return nil, fmt.Errorf("calling config hook: %w", hookErr)
		}

		if o.validate {
			// Validate again since config hooks can modify the config.
			validateErr := types.ValidateConfigSpec(*c)
			if validateErr != nil {
				return nil, fmt.Errorf("validating config after config hooks: %w", validateErr)
			}
		}
	}

	err := store.Create(c)
	if err != nil {
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
		return nil, errors.New("no config name provided")
	}

	c, err := store.NewConfig(name)
	if err != nil {
		return nil, err
	}

	if err = store.Get(c); err != nil {
		return nil, fmt.Errorf("getting config from store: %w", err)
	}

	var expName string

	if c.Kind == "Experiment" {
		var exp *types.Experiment
		exp, err = types.DecodeExperimentFromConfig(*c)
		if err != nil {
			return nil, fmt.Errorf("decoding experiment from config: %w", err)
		}

		if !force && exp.Running() {
			return nil, errors.New("cannot edit running experiment")
		}

		expName = exp.Spec.ExperimentName()

		// Don't allow users to edit the experiment name field.
		delete(c.Spec, "experimentName")
	}

	var body []byte
	body, err = yaml.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshaling config to YAML: %w", err)
	}

	body, err = editor.EditData(body)
	if err != nil {
		return nil, fmt.Errorf("editing config: %w", err)
	}

	if err = yaml.Unmarshal(body, c); err != nil {
		return nil, fmt.Errorf("unmarshaling config as YAML: %w", err)
	}

	if c.Kind == "Experiment" {
		c.Spec["experimentName"] = expName
	}

	if err = Update(name, c); err != nil {
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

	if err = store.Get(old); err != nil {
		return fmt.Errorf("getting config to update: %w", err)
	}

	c.Metadata.Created = old.Metadata.Created

	if err = types.ValidateConfigSpec(*c); err != nil {
		return fmt.Errorf("validating config: %w", err)
	}

	for _, hook := range hooks[c.Kind] {
		hookErr := hook("update", c)
		if hookErr != nil {
			return fmt.Errorf("calling config hook: %w", hookErr)
		}

		// Validate again since config hooks can modify the config.
		err = types.ValidateConfigSpec(*c)
		if err != nil {
			return fmt.Errorf("validating config after config hooks: %w", err)
		}
	}

	if err = store.Update(c); err != nil {
		if errors.Is(err, store.ErrNotExist) { // name changed during update
			createErr := store.Create(c)
			if createErr != nil {
				return fmt.Errorf("renaming updated config in store: %w", createErr)
			}

			deleteErr := store.Delete(old)
			if deleteErr != nil {
				_ = store.Delete(c) // don't offer a path to creation via updates

				return fmt.Errorf("renaming updated config in store: %w", deleteErr)
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
		return errors.New("no config name provided")
	}

	var errs error

	if name == "all" {
		configs, _ := List("all")

		for _, c := range configs {
			deleteErr := store.Delete(&c)
			if deleteErr != nil {
				errs = multierror.Append(
					errs,
					fmt.Errorf("deleting config %s/%s: %w", c.Kind, c.Metadata.Name, deleteErr),
				)

				continue
			}

			for _, hook := range hooks[c.Kind] {
				hookErr := hook("delete", &c)
				if hookErr != nil {
					errs = multierror.Append(
						errs,
						fmt.Errorf(
							"executing delete experiment hook for config %s/%s: %w",
							c.Kind,
							c.Metadata.Name,
							hookErr,
						),
					)
				}
			}
		}

		return errs
	}

	c, err := Get(name, false)
	if err != nil {
		return fmt.Errorf("getting config %s: %w", name, err)
	}

	if err = store.Delete(c); err != nil {
		return fmt.Errorf("deleting config %s: %w", name, err)
	}

	for _, hook := range hooks[c.Kind] {
		hookErr := hook("delete", c)
		if hookErr != nil {
			errs = multierror.Append(
				errs,
				fmt.Errorf("executing delete experiment hook for config %s: %w", name, hookErr),
			)
		}
	}

	return errs
}

// IsConfigNotModified returns a boolean indicating whether the error is known
// to report that a config was not modified during editing. It is satisfied by
// editor.ErrNoChange.
func IsConfigNotModified(err error) bool {
	return errors.Is(err, editor.ErrNoChange)
}
