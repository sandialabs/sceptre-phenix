package cmd

import (
	"testing"

	"github.com/spf13/viper"
)

func TestDerivedBaseDir(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		phenixBase string
		sub        string
		want       string
	}{
		{name: "unset injects", value: "", phenixBase: "/phenix", sub: "injects", want: "/phenix/injects"},
		{name: "unset topologies", value: "", phenixBase: "/phenix", sub: "topologies", want: "/phenix/topologies"},
		{name: "phenix base with trailing slash", value: "", phenixBase: "/srv/phenix/", sub: "injects", want: "/srv/phenix/injects"},
		{name: "explicit absolute value", value: "/data/injects", phenixBase: "/phenix", sub: "injects", want: "/data/injects"},
		{name: "explicit value is verbatim", value: "topologies/", phenixBase: "/phenix", sub: "topologies", want: "topologies/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := derivedBaseDir(tt.value, tt.phenixBase, tt.sub); got != tt.want {
				t.Errorf("derivedBaseDir(%q, %q, %q) = %q, want %q", tt.value, tt.phenixBase, tt.sub, got, tt.want)
			}
		})
	}
}

func TestBaseDirFlags(t *testing.T) {
	tests := []struct {
		flag  string
		env   string
		sub   string
		usage string
	}{
		{
			flag: "base-dir.injects",
			env:  "PHENIX_BASE_DIR_INJECTS",
			sub:  "injects",
			usage: "base directory for staged workflow injects (default: <base-dir.phenix>/injects; " +
				"workflow apply uses the server's value unless this flag is given)",
		},
		{
			flag:  "base-dir.topologies",
			env:   "PHENIX_BASE_DIR_TOPOLOGIES",
			sub:   "topologies",
			usage: "base directory for topology directories (default: <base-dir.phenix>/topologies)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			f := rootCmd.PersistentFlags().Lookup(tt.flag)
			if f == nil {
				t.Fatalf("--%s is not a root persistent flag", tt.flag)
			}

			if f.DefValue != "" {
				t.Errorf("--%s default = %q, want empty so it derives from base-dir.phenix", tt.flag, f.DefValue)
			}

			if f.Usage != tt.usage {
				t.Errorf("--%s usage = %q, want %q", tt.flag, f.Usage, tt.usage)
			}

			// Viper ranks env above the config file, so this holds whatever the
			// local config.yaml contains.
			value := "/from/env/" + tt.sub
			t.Setenv(tt.env, value)

			if got := viper.GetString(tt.flag); got != value {
				t.Errorf("viper.GetString(%q) with %s set = %q, want %q", tt.flag, tt.env, got, value)
			}

			// A flag given on the command line beats the environment, which
			// holds only when the flag is bound to viper.
			origValue, origChanged := f.Value.String(), f.Changed
			t.Cleanup(func() {
				_ = f.Value.Set(origValue)
				f.Changed = origChanged
			})

			flagValue := "/from/flag/" + tt.sub
			if err := f.Value.Set(flagValue); err != nil {
				t.Fatalf("setting --%s: %v", tt.flag, err)
			}

			f.Changed = true

			if got := viper.GetString(tt.flag); got != flagValue {
				t.Errorf(
					"viper.GetString(%q) with --%s set = %q, want %q; the flag is not bound to viper",
					tt.flag, tt.flag, got, flagValue,
				)
			}
		})
	}
}
