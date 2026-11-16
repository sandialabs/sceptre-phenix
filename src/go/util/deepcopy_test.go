package util_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/util"
)

// genValue draws a scalar, or at most depth levels of map[string]any and []any.
func genValue(depth int) *rapid.Generator[any] {
	scalar := rapid.OneOf(
		rapid.Map(rapid.Int(), func(v int) any { return v }),
		rapid.Map(rapid.String(), func(v string) any { return v }),
		rapid.Map(rapid.Bool(), func(v bool) any { return v }),
	)

	if depth == 0 {
		return scalar
	}

	return rapid.OneOf(
		scalar,
		rapid.Map(genMap(depth-1), func(v map[string]any) any { return v }),
		rapid.Map(rapid.SliceOfN(genValue(depth-1), 0, 4), func(v []any) any { return v }),
	)
}

func genMap(depth int) *rapid.Generator[map[string]any] {
	return rapid.MapOfN(rapid.StringMatching(`[a-e]{1,2}`), genValue(depth), 0, 4)
}

// clone copies v independently of util.CopyableMap, so the test does not check DeepCopy with itself.
func clone(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = clone(e)
		}

		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = clone(e)
		}

		return out
	}

	return v
}

// change replaces every scalar in v and adds a key to every map, at every level.
func change(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, e := range v {
			v[k] = change(e)
		}

		v["changed"] = true

		return v
	case []any:
		for i, e := range v {
			v[i] = change(e)
		}

		return v
	}

	return "changed"
}

// A deep copy equals its input, and a change to the copy at any level leaves the input unchanged.
func TestDeepCopyProperties(t *testing.T) {
	t.Parallel()

	equateEmpty := cmpopts.EquateEmpty()

	t.Run("map", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			in := genMap(3).Draw(t, "in")
			want := clone(in)

			got := util.CopyableMap(in).DeepCopy()
			if diff := cmp.Diff(want, any(got), equateEmpty); diff != "" {
				t.Fatalf("CopyableMap(%v).DeepCopy() mismatch (-want +got):\n%s", in, diff)
			}

			change(got)

			if diff := cmp.Diff(want, any(in), equateEmpty); diff != "" {
				t.Fatalf("input after a change to its copy mismatch (-want +got):\n%s", diff)
			}
		})
	})

	t.Run("slice", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			in := rapid.SliceOfN(genValue(3), 0, 4).Draw(t, "in")
			want := clone(in)

			got := util.CopyableSlice(in).DeepCopy()
			if diff := cmp.Diff(want, any(got), equateEmpty); diff != "" {
				t.Fatalf("CopyableSlice(%v).DeepCopy() mismatch (-want +got):\n%s", in, diff)
			}

			change(got)

			if diff := cmp.Diff(want, any(in), equateEmpty); diff != "" {
				t.Fatalf("input after a change to its copy mismatch (-want +got):\n%s", diff)
			}
		})
	})
}

// DeepCopy of nil gives an empty, non-nil value.
func TestDeepCopyNil(t *testing.T) {
	t.Parallel()

	if got := util.CopyableMap(nil).DeepCopy(); got == nil || len(got) != 0 {
		t.Errorf("CopyableMap(nil).DeepCopy() = %#v, want an empty map", got)
	}

	if got := util.CopyableSlice(nil).DeepCopy(); got == nil || len(got) != 0 {
		t.Errorf("CopyableSlice(nil).DeepCopy() = %#v, want an empty slice", got)
	}
}
