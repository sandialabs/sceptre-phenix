package scorchmd

import (
	"math"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util"
)

// Generate fails unless exactly one distribution is given, and fails for a
// uniform distribution whose maximum is not above its minimum.
func TestGenerateErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]DistributionSpec{
		"no distribution": {},
		"two distributions": {
			Uniform: &UniformDistribution{Maximum: 1}, Gaussian: &GaussianDistribution{},
		},
		"three distributions": {
			Uniform: &UniformDistribution{}, Gaussian: &GaussianDistribution{}, Exponential: &ExponentialDistribution{},
		},
		"uniform maximum equal to minimum": {Uniform: &UniformDistribution{Minimum: 5, Maximum: 5}},
		"uniform maximum below minimum":    {Uniform: &UniformDistribution{Minimum: 5, Maximum: 1}},
	}

	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, err := spec.Generate(); err == nil {
				t.Errorf("Generate(%s) = %v, want an error", name, got)
			}
		})
	}
}

// Generate draws a float64 from the distribution it is given, or an int64
// rounded from it for type int. A uniform distribution with no bounds draws
// from [0, 10), and an exponential one never draws a negative value.
func TestGenerate(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		spec     DistributionSpec
		min, max float64
	}{
		"uniform":                {spec: DistributionSpec{Uniform: &UniformDistribution{Minimum: 2, Maximum: 3}}, min: 2, max: 3},
		"uniform without bounds": {spec: DistributionSpec{Uniform: &UniformDistribution{}}, max: 10},
		"uniform int": {
			spec: DistributionSpec{Type: "int", Uniform: &UniformDistribution{Minimum: 2, Maximum: 3}},
			min:  2,
			max:  3,
		},
		"gaussian":                 {spec: DistributionSpec{Gaussian: &GaussianDistribution{Mean: 100, StdDev: 0.001}}, min: 99, max: 101},
		"gaussian with no setting": {spec: DistributionSpec{Gaussian: &GaussianDistribution{}}, min: math.Inf(-1), max: math.Inf(1)},
		"exponential":              {spec: DistributionSpec{Exponential: &ExponentialDistribution{Mean: 3}}, max: math.Inf(1)},
		"exponential int":          {spec: DistributionSpec{Type: "int", Exponential: &ExponentialDistribution{}}, max: math.Inf(1)},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for range 50 {
				got, err := tc.spec.Generate()
				if err != nil {
					t.Fatalf("Generate(%s) error = %v", name, err)
				}

				var val float64

				switch v := got.(type) {
				case int64:
					if tc.spec.Type != "int" {
						t.Fatalf("Generate(%s) = %v (int64), want a float64", name, got)
					}

					val = float64(v)
				case float64:
					if tc.spec.Type == "int" {
						t.Fatalf("Generate(%s) = %v (float64), want an int64", name, got)
					}

					val = v
				default:
					t.Fatalf("Generate(%s) = %v (%T), want a number", name, got, got)
				}

				if val < tc.min || val > tc.max || math.IsNaN(val) {
					t.Fatalf("Generate(%s) = %v, want a value in [%v, %v]", name, val, tc.min, tc.max)
				}
			}
		})
	}
}

// A uniform distribution draws a value in [minimum, maximum).
func TestGenerateUniformBounds(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		lo := rapid.Float64Range(-1e6, 1e6).Draw(t, "minimum")
		hi := lo + rapid.Float64Range(1e-3, 1e6).Draw(t, "width")

		if lo == 0 && hi == 0 {
			t.Skip("no bounds selects the default range")
		}

		spec := DistributionSpec{Uniform: &UniformDistribution{Minimum: lo, Maximum: hi}}

		got, err := spec.Generate()
		if err != nil {
			t.Fatalf("Generate(uniform [%v, %v)) error = %v", lo, hi, err)
		}

		if v, ok := got.(float64); !ok || v < lo || v >= hi {
			t.Fatalf("Generate(uniform [%v, %v)) = %v, want a float64 in that range", lo, hi, got)
		}
	})
}

// ResolveReplacements picks a value for each key from its list or its
// distribution.
func TestResolveReplacements(t *testing.T) {
	t.Parallel()

	choices := []any{"a", "b", 3}

	for range 20 {
		got, err := ResolveReplacements(map[string]any{
			"$LIST": choices,
			"$DIST": map[string]any{"type": "int", "uniform": map[string]any{"minimum": 1, "maximum": 2}},
		})
		if err != nil {
			t.Fatalf("ResolveReplacements() error = %v", err)
		}

		if len(got) != 2 {
			t.Errorf("ResolveReplacements() = %v, want 2 keys", got)
		}

		if !slices.Contains(choices, got["$LIST"]) {
			t.Errorf("ResolveReplacements() $LIST = %v, want one of %v", got["$LIST"], choices)
		}

		if v, ok := got["$DIST"].(int64); !ok || v < 1 || v > 2 {
			t.Errorf("ResolveReplacements() $DIST = %v (%T), want an int64 in [1, 2]", got["$DIST"], got["$DIST"])
		}
	}

	got, err := ResolveReplacements(nil)
	if err != nil || len(got) != 0 {
		t.Errorf("ResolveReplacements(nil) = %v, %v; want no replacements", got, err)
	}
}

// ResolveReplacements fails for an empty list, a distribution that does not
// decode or generate, and a value that is neither a list nor a distribution.
func TestResolveReplacementsErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]any{
		"empty list":                   []any{},
		"distribution does not decode": map[string]any{"uniform": "wide"},
		"no distribution":              map[string]any{"type": "int"},
		"uniform with bad bounds":      map[string]any{"uniform": map[string]any{"minimum": 3, "maximum": 1}},
		"unsupported type":             "a",
	}

	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, err := ResolveReplacements(map[string]any{"$K": spec}); err == nil {
				t.Errorf("ResolveReplacements(%v) = %v, want an error", spec, got)
			}
		})
	}
}

// MergeReplacements takes the keys of both, with the values of override for
// keys in both, and changes neither.
func TestMergeReplacements(t *testing.T) {
	t.Parallel()

	base := ResolvedReplacements{"$A": 1, "$B": 2}
	override := ResolvedReplacements{"$B": 20, "$C": 30}

	got := MergeReplacements(base, override)

	if diff := cmp.Diff(ResolvedReplacements{"$A": 1, "$B": 20, "$C": 30}, got); diff != "" {
		t.Errorf("MergeReplacements() mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(ResolvedReplacements{"$A": 1, "$B": 2}, base); diff != "" {
		t.Errorf("MergeReplacements() changed base (-want +got):\n%s", diff)
	}

	if got := MergeReplacements(nil, nil); got == nil || len(got) != 0 {
		t.Errorf("MergeReplacements(nil, nil) = %#v, want an empty map", got)
	}
}

// ApplyReplacements replaces a string that is a key with the key's value,
// and each key in other strings with the value's text, in nested maps and
// lists too. It keeps other values.
func TestApplyReplacements(t *testing.T) {
	t.Parallel()

	resolved := ResolvedReplacements{"$PORT": int64(8080), "$HOST": "web"}

	meta := ComponentMetadata{
		"port":  "$PORT",
		"url":   "http://$HOST:$PORT/",
		"count": 3,
		"nested": map[string]any{
			"list": []any{"$HOST", "x-$HOST", true, map[string]any{"deep": "$PORT"}},
		},
	}

	want := ComponentMetadata{
		"port":  int64(8080),
		"url":   "http://web:8080/",
		"count": 3,
		"nested": map[string]any{
			"list": []any{"web", "x-web", true, map[string]any{"deep": int64(8080)}},
		},
	}

	if diff := cmp.Diff(want, ApplyReplacements(meta, resolved)); diff != "" {
		t.Errorf("ApplyReplacements() mismatch (-want +got):\n%s", diff)
	}

	if got := ApplyReplacements(meta, nil); cmp.Diff(meta, got) != "" {
		t.Errorf("ApplyReplacements(no replacements) = %v, want %v", got, meta)
	}
}

// ApplyReplacements leaves its input unchanged, and a string that is a whole
// key takes the key's value with its type.
func TestApplyReplacementsProperties(t *testing.T) {
	t.Parallel()

	keys := []string{"$A", "$B"}
	strs := rapid.SampledFrom([]string{"$A", "$B", "x", "$A-$B", "pre $A"})

	value := rapid.OneOf(
		strs.AsAny(),
		rapid.IntRange(-5, 5).AsAny(),
		rapid.SliceOfN(strs.AsAny(), 0, 3).AsAny(),
	)

	rapid.Check(t, func(t *rapid.T) {
		resolved := ResolvedReplacements{}
		for _, k := range keys {
			resolved[k] = rapid.OneOf(rapid.Int().AsAny(), rapid.StringMatching(`[a-z]{0,4}`).AsAny()).Draw(t, k)
		}

		meta := ComponentMetadata(rapid.MapOfN(rapid.StringMatching(`[a-z]{1,3}`), value, 0, 5).Draw(t, "meta"))

		before := util.CopyableMap(meta).DeepCopy()

		got := ApplyReplacements(meta, resolved)

		if diff := cmp.Diff(before, map[string]any(meta)); diff != "" {
			t.Fatalf("ApplyReplacements() changed its input (-want +got):\n%s", diff)
		}

		for k, v := range meta {
			if s, ok := v.(string); ok {
				if r, isKey := resolved[s]; isKey && cmp.Diff(r, got[k]) != "" {
					t.Fatalf("ApplyReplacements()[%q] = %#v, want %#v for key %s", k, got[k], r, s)
				}
			}
		}
	})
}

// ApplyReplacements replaces the keys in a string in one pass, so a value
// that holds a key keeps it, and the longest key wins where keys overlap. It
// does not replace an empty key in a string.
func TestApplyReplacementsOnePass(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		s        string
		resolved ResolvedReplacements
		want     any
	}{
		"value holds a key": {s: "x{{A}}", resolved: ResolvedReplacements{"{{A}}": "{{B}}", "{{B}}": "y"}, want: "x{{B}}"},
		"longest key":       {s: "aaa", resolved: ResolvedReplacements{"a": "", "aa": "b"}, want: "b"},
		"key in a key":      {s: "$AB-$A", resolved: ResolvedReplacements{"$A": 1, "$AB": 2}, want: "2-1"},
		"empty key":         {s: "abc", resolved: ResolvedReplacements{"": "-", "b": "B"}, want: "aBc"},
		"empty string":      {s: "", resolved: ResolvedReplacements{"": "empty"}, want: "empty"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for range 5 {
				if got := ApplyReplacements(ComponentMetadata{"x": tc.s}, tc.resolved)["x"]; got != tc.want {
					t.Fatalf("ApplyReplacements(%q, %v) = %#v, want %#v", tc.s, tc.resolved, got, tc.want)
				}
			}
		})
	}
}
