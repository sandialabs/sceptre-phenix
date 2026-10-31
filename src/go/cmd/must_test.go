package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/pflag"
)

func TestMustGet(t *testing.T) {
	t.Parallel()

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("name", "", "")
	flags.Bool("force", false, "")
	flags.Int("count", 0, "")
	flags.StringArray("tag", nil, "")

	err := flags.Parse([]string{"--name", "foo", "--force", "--count", "3", "--tag", "a", "--tag", "b"})
	if err != nil {
		t.Fatalf("parsing flags: %v", err)
	}

	if got := mustGet(flags.GetString, "name"); got != "foo" {
		t.Errorf("string flag: got %q, want %q", got, "foo")
	}

	if got := mustGet(flags.GetBool, "force"); !got {
		t.Error("bool flag: got false, want true")
	}

	if got := mustGet(flags.GetInt, "count"); got != 3 {
		t.Errorf("int flag: got %d, want 3", got)
	}

	if diff := cmp.Diff([]string{"a", "b"}, mustGet(flags.GetStringArray, "tag")); diff != "" {
		t.Errorf("string array flag mismatch (-want +got):\n%s", diff)
	}
}

func TestMustGetPanicsWithFlagName(t *testing.T) {
	t.Parallel()

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("name", "", "")

	tests := map[string]struct {
		flag string
		call func()
	}{
		"undefined": {"missing", func() { mustGet(flags.GetString, "missing") }},
		"mistyped":  {"name", func() { mustGet(flags.GetBool, "name") }},
	}

	for label, test := range tests {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			defer func() {
				msg := fmt.Sprint(recover())
				if want := "Getting value for " + test.flag + ": "; !strings.HasPrefix(msg, want) {
					t.Errorf("expected panic starting %q, got %q", want, msg)
				}
			}()

			test.call()
		})
	}
}
