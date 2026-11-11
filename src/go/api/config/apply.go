package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"phenix/store"
	"phenix/types"
)

// ApplyAction is what [Apply] did, or in a dry run would do, to a config.
type ApplyAction string

// The actions of [Apply].
const (
	ApplyCreated   ApplyAction = "created"
	ApplyUpdated   ApplyAction = "updated"
	ApplyUnchanged ApplyAction = "unchanged"
)

// ErrApplyKind is the error of [Compare] and [Apply] for a config of a kind
// that they do not take.
var ErrApplyKind = errors.New("config kind cannot be applied from a file")

// applyKinds are the kinds that [Apply] and [Compare] take, in the order
// [ApplyOrder] puts them: a scenario and an experiment refer to a topology,
// and an experiment to a scenario. A User config holds password hashes and
// tokens, which a file does not hold, so it is not taken.
var applyKinds = []string{"Topology", "Scenario", kindExperiment, "Image", "Role"} //nolint:gochecknoglobals // constant list

// serverAnnotations are the annotation keys that phenix sets on a config, which
// a comparison ignores (see [RegisterServerAnnotation]).
var serverAnnotations []string //nolint:gochecknoglobals // registered at init, as hooks are

// RegisterServerAnnotation makes [Compare] ignore the annotation key, which
// phenix sets on a config and keeps through an update. Call it from an init
// function, as [RegisterConfigHook].
func RegisterServerAnnotation(key string) {
	serverAnnotations = append(serverAnnotations, key)
}

// ApplyOrder compares the kinds of a and b in the order that [Apply] must
// take them: Topology, Scenario, Experiment, Image, Role, then other kinds.
// Use it with [slices.SortStableFunc] to keep the file order in each kind.
func ApplyOrder(a, b string) int {
	rank := func(kind string) int {
		for i, k := range applyKinds {
			if strings.EqualFold(k, kind) {
				return i
			}
		}

		return len(applyKinds)
	}

	return rank(a) - rank(b)
}

// Comparison is a config from a file compared with the config of the same
// kind and name in the store.
type Comparison struct {
	// Name is the kind/name of the config, with the kind as the store spells it.
	Name string
	// Stored is the config in the store, nil when the store does not hold it.
	Stored *store.Config
	// StoredYAML and FileYAML are the normalized YAML of the two configs.
	// StoredYAML is nil when Stored is nil.
	StoredYAML, FileYAML []byte
}

// Changed reports whether the file differs from the store: the store does
// not hold the config, or holds it with a different normalized form.
func (c *Comparison) Changed() bool {
	return c.Stored == nil || string(c.StoredYAML) != string(c.FileYAML)
}

// Compare compares c, a config from a file, with the stored config of its kind
// and name: apiVersion, kind, name, annotations and spec. It ignores the fields
// phenix sets: created, updated, revision, status, an experiment's name and the
// annotations of [RegisterServerAnnotation]. It sets c.Kind as the store spells
// it. The error wraps [ErrApplyKind] for a kind it does not take.
func Compare(c *store.Config) (*Comparison, error) {
	ref, err := applyRef(c)
	if err != nil {
		return nil, err
	}

	file, err := normalizedYAML(c)
	if err != nil {
		return nil, err
	}

	cmp := &Comparison{Name: ref.FullName(), Stored: nil, StoredYAML: nil, FileYAML: file}

	if err := store.Get(ref); err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return cmp, nil
		}

		return nil, fmt.Errorf("getting config %s: %w", ref.FullName(), storeFailed(err))
	}

	stored, err := normalizedYAML(ref)
	if err != nil {
		return nil, err
	}

	cmp.Stored, cmp.StoredYAML = ref, stored

	return cmp, nil
}

// applyRef returns a new config with the kind and name of c, and sets c.Kind
// to the kind as the store spells it.
func applyRef(c *store.Config) (*store.Config, error) {
	if c.Metadata.Name == "" {
		return nil, errors.New("config has no metadata.name")
	}

	ref, err := store.NewConfig(c.Kind + "/" + c.Metadata.Name)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrApplyKind, err)
	}

	if !slices.Contains(applyKinds, ref.Kind) {
		return nil, fmt.Errorf("%w: kind %s (use one of %s)", ErrApplyKind, ref.Kind, strings.Join(applyKinds, ", "))
	}

	c.Kind = ref.Kind

	return ref, nil
}

// normalizedView is the part of a config that a comparison looks at.
type normalizedView struct {
	Version  string `json:"apiVersion"`
	Kind     string `json:"kind"`
	Metadata struct {
		Name        string            `json:"name"`
		Annotations store.Annotations `json:"annotations,omitempty"`
	} `json:"metadata"`
	Spec map[string]any `json:"spec,omitempty"`
}

// normalizedYAML returns the parts of c that a comparison looks at as YAML
// with sorted keys. A JSON round trip first gives numbers and maps one form,
// whether they came from YAML, JSON or the store.
func normalizedYAML(c *store.Config) ([]byte, error) {
	var view normalizedView

	view.Version, view.Kind, view.Metadata.Name = c.Version, c.Kind, c.Metadata.Name

	if len(c.Metadata.Annotations) > 0 {
		view.Metadata.Annotations = maps.Clone(c.Metadata.Annotations)
		for _, key := range serverAnnotations {
			delete(view.Metadata.Annotations, key)
		}
	}

	if c.Spec != nil {
		view.Spec = maps.Clone(c.Spec)
		if c.Kind == kindExperiment {
			delete(view.Spec, "experimentName")
		}
	}

	body, err := json.Marshal(view)
	if err != nil {
		return nil, fmt.Errorf("normalizing config %s: %w", c.FullName(), err)
	}

	var generic any
	if err := json.Unmarshal(body, &generic); err != nil {
		return nil, fmt.Errorf("normalizing config %s: %w", c.FullName(), err)
	}

	var out bytes.Buffer

	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2) //nolint:mnd // the indent of phenix's own YAML files

	if err := enc.Encode(generic); err != nil {
		return nil, fmt.Errorf("normalizing config %s: %w", c.FullName(), err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("normalizing config %s: %w", c.FullName(), err)
	}

	return out.Bytes(), nil
}

// ApplyOption changes what [Apply] does.
type ApplyOption func(*applyOptions)

type applyOptions struct {
	dryRun, skipCreateValidation bool
	ctx                          context.Context
}

// ApplyWithContext gives ctx to the config hooks of a new config, as
// [CreateWithContext] does.
func ApplyWithContext(ctx context.Context) ApplyOption {
	return func(o *applyOptions) { o.ctx = ctx }
}

// ApplyDryRun makes [Apply] check the config and report the action, without
// a change to the store. It does not run the config hooks.
func ApplyDryRun() ApplyOption {
	return func(o *applyOptions) { o.dryRun = true }
}

// ApplySkipCreateValidation makes [Apply] create a new config without the
// schema validation, as config create without validation does. An update is
// always validated, as [Update] does.
func ApplySkipCreateValidation() ApplyOption {
	return func(o *applyOptions) { o.skipCreateValidation = true }
}

// Apply creates c, a config from a file, when it is not stored, updates the
// stored config when it differs (see [Compare]), and else does nothing. As in
// config edit, an update goes through [Update], refuses a running experiment
// with [ErrRunningExperiment], keeps the stored status and experiment name, and
// fails with [store.ErrConflict] when the config changed after Apply read it.
func Apply(c *store.Config, opts ...ApplyOption) (ApplyAction, error) {
	o := applyOptions{ctx: context.Background()} //nolint:exhaustruct // functional options pattern
	for _, opt := range opts {
		opt(&o)
	}

	cmp, err := Compare(c)
	if err != nil {
		return "", err
	}

	if cmp.Stored == nil {
		return applyCreate(c, o)
	}

	if !cmp.Changed() {
		return ApplyUnchanged, nil
	}

	stored := cmp.Stored

	if stored.Kind == kindExperiment {
		exp, err := types.DecodeExperimentFromConfig(*stored)
		if err != nil {
			return "", fmt.Errorf("decoding experiment from config: %w", err)
		}

		if exp.Running() {
			return "", fmt.Errorf(
				"cannot update the config of experiment %s: %w; stop it first",
				stored.Metadata.Name,
				ErrRunningExperiment,
			)
		}

		if c.Spec != nil {
			c.Spec["experimentName"] = exp.Spec.ExperimentName()
		}
	}

	c.Status = stored.Status
	c.Metadata.Revision = 0
	c.IfRevision = stored.Metadata.Revision

	if o.dryRun {
		if err := Validate(c); err != nil {
			return "", err
		}

		return ApplyUpdated, nil
	}

	if err := Update(stored.FullName(), c); err != nil {
		return "", fmt.Errorf("updating config %s: %w", stored.FullName(), err)
	}

	return ApplyUpdated, nil
}

func applyCreate(c *store.Config, o applyOptions) (ApplyAction, error) {
	if o.dryRun {
		if !o.skipCreateValidation {
			if err := Validate(c); err != nil {
				return "", err
			}
		}

		return ApplyCreated, nil
	}

	opts := []CreateOption{CreateFromConfig(c), CreateWithContext(o.ctx)}
	if !o.skipCreateValidation {
		opts = append(opts, CreateWithValidation())
	}

	if _, err := Create(opts...); err != nil {
		return "", err
	}

	return ApplyCreated, nil
}
