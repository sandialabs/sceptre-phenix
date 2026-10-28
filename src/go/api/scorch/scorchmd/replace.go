package scorchmd

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/mitchellh/mapstructure"

	"phenix/util"
)

// ResolvedReplacements holds the randomly selected value for each replacement key.
type ResolvedReplacements map[string]any

type UniformDistribution struct {
	Minimum float64 `mapstructure:"minimum"`
	Maximum float64 `mapstructure:"maximum"`
}

type GaussianDistribution struct {
	Mean   float64 `mapstructure:"mean"`
	StdDev float64 `mapstructure:"stddev"`
}

type ExponentialDistribution struct {
	Mean float64 `mapstructure:"mean"`
}

type DistributionSpec struct {
	Type        string                   `mapstructure:"type"`
	Uniform     *UniformDistribution     `mapstructure:"uniform"`
	Gaussian    *GaussianDistribution    `mapstructure:"gaussian"`
	Exponential *ExponentialDistribution `mapstructure:"exponential"`
}

func (d *DistributionSpec) Generate() (any, error) {
	count := 0
	if d.Uniform != nil {
		count++
	}

	if d.Gaussian != nil {
		count++
	}

	if d.Exponential != nil {
		count++
	}

	if count == 0 {
		return nil, errors.New("no distribution specified (uniform, gaussian, or exponential)")
	}

	if count > 1 {
		return nil, errors.New("cannot specify multiple distributions")
	}

	var val float64

	switch {
	case d.Gaussian != nil:
		mean := 10.0
		stddev := 2.0

		if d.Gaussian.Mean != 0 {
			mean = d.Gaussian.Mean
		}

		if d.Gaussian.StdDev != 0 {
			stddev = d.Gaussian.StdDev
		}

		val = rand.NormFloat64()*stddev + mean //nolint:gosec // G404 sample values, not secrets
	case d.Exponential != nil:
		mean := 10.0
		if d.Exponential.Mean != 0 {
			mean = d.Exponential.Mean
		}

		val = rand.ExpFloat64() * mean
	default:
		minVal := d.Uniform.Minimum

		maxVal := d.Uniform.Maximum
		if minVal == 0 && maxVal == 0 {
			minVal = 0.0
			maxVal = 10.0
		}

		if maxVal <= minVal {
			return nil, fmt.Errorf(
				"uniform maximum (%v) must be greater than minimum (%v)",
				maxVal,
				minVal,
			)
		}

		val = minVal + rand.Float64()*(maxVal-minVal) //nolint:gosec // G404 sample values, not secrets
	}

	if d.Type == "int" {
		return int64(math.Round(val)), nil
	}

	return val, nil
}

// ResolveReplacements selects a random value for each key in the replace map.
func ResolveReplacements(replace map[string]any) (ResolvedReplacements, error) {
	resolved := make(ResolvedReplacements)

	for key, spec := range replace {
		switch v := spec.(type) {
		// Simple list of values to choose from
		case []any:
			if len(v) == 0 {
				return nil, fmt.Errorf("replacement key %q has empty list", key)
			}

			resolved[key] = v[rand.IntN(len(v))] //nolint:gosec // G404 sample values, not secrets

		// Random distribution
		case map[string]any:
			var dist DistributionSpec
			if err := mapstructure.Decode(v, &dist); err != nil {
				return nil, fmt.Errorf("decoding distribution spec for key %q: %w", key, err)
			}

			val, err := dist.Generate()
			if err != nil {
				return nil, fmt.Errorf("generating value for key %q: %w", key, err)
			}

			resolved[key] = val

		// Error
		default:
			return nil, fmt.Errorf("replacement key %q has unsupported type %T", key, spec)
		}
	}

	return resolved, nil
}

// ApplyReplacements applies the resolved replacements to the given metadata recursively.
func ApplyReplacements(meta ComponentMetadata, resolved ResolvedReplacements) ComponentMetadata {
	if len(resolved) == 0 {
		return meta
	}

	copied := util.CopyableMap(meta).DeepCopy()

	return applyToMap(copied, resolved, replacer(resolved))
}

func applyToMap(m map[string]any, resolved ResolvedReplacements, r *strings.Replacer) map[string]any {
	for k, v := range m {
		m[k] = applyToValue(v, resolved, r)
	}

	return m
}

func applyToSlice(s []any, resolved ResolvedReplacements, r *strings.Replacer) []any {
	for i, v := range s {
		s[i] = applyToValue(v, resolved, r)
	}

	return s
}

func applyToValue(v any, resolved ResolvedReplacements, r *strings.Replacer) any {
	switch val := v.(type) {
	case string:
		return applyToString(val, resolved, r)
	case map[string]any:
		return applyToMap(val, resolved, r)
	case []any:
		return applyToSlice(val, resolved, r)
	default:
		return v
	}
}

func applyToString(s string, resolved ResolvedReplacements, r *strings.Replacer) any {
	// Check if entire string is a replacement key - return typed value
	if val, ok := resolved[s]; ok {
		return val
	}

	return r.Replace(s)
}

// replacer replaces each non-empty key with its value's text in one pass. Where
// keys start at the same place, the longest key is replaced.
func replacer(resolved ResolvedReplacements) *strings.Replacer {
	keys := slices.SortedFunc(maps.Keys(resolved), func(a, b string) int {
		return cmp.Or(len(b)-len(a), strings.Compare(a, b))
	})

	pairs := make([]string, 0, len(keys)*2) //nolint:mnd // a key and its value

	for _, key := range keys {
		if key != "" {
			pairs = append(pairs, key, fmt.Sprintf("%v", resolved[key]))
		}
	}

	return strings.NewReplacer(pairs...)
}

// MergeReplacements combines two sets of replacements.
// Values in 'override' take precedence over values in 'base'.
func MergeReplacements(base, override ResolvedReplacements) ResolvedReplacements {
	merged := make(ResolvedReplacements)

	maps.Copy(merged, base)

	maps.Copy(merged, override)

	return merged
}
