// Package runtimesettings reads and changes the runtime settings that phenix
// reads through viper from its flags, PHENIX_ environment variables and YAML
// config file. phenix settings and the /runtimeSettings routes use it. A change
// is written to the config file, and the config watcher of phenix ui applies
// the live settings (see Live) at once; the others wait for a restart.
package runtimesettings

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Where the value of a setting comes from, in viper's order.
const (
	SourceFlag    = "flag"
	SourceEnv     = "env"
	SourceFile    = "config file"
	SourceDefault = "default"
)

// Redacted replaces the value of a secret setting.
const Redacted = "<redacted>"

var (
	// ErrNotFound is the error for a setting key that has no value and that
	// no flag declares.
	ErrNotFound = errors.New("setting not found")

	// ErrNoConfigFile is the error of Unset when the config file does not
	// exist.
	ErrNoConfigFile = errors.New("no configuration file found")
)

// liveSettings are the settings, and prefixes ending in a dot, that the config
// watcher of phenix ui applies at once.
//
//nolint:gochecknoglobals // constant list
var liveSettings = []string{"log.", "ui.logs.level", "ui.users", "experiment.disk-check", "minimega.read-connections"}

// secretSettings are the settings whose values Redact hides.
//
//nolint:gochecknoglobals // constant list
var secretSettings = []string{"ui.jwt-signing-key", "ui.users"}

// Live reports whether the config watcher of phenix ui applies a change to key
// at once. A change to any other setting takes effect when phenix ui restarts.
func Live(key string) bool {
	for _, live := range liveSettings {
		if key == live || (strings.HasSuffix(live, ".") && strings.HasPrefix(key, live)) {
			return true
		}
	}

	return false
}

// Secret reports whether key holds a secret, such as the JWT signing key.
func Secret(key string) bool {
	return slices.Contains(secretSettings, key)
}

// Redact returns value, or Redacted for the value of a secret setting that is
// set. An unset secret stays as it is, so an empty JWT signing key still shows.
func Redact(key string, value any) any {
	if !Secret(key) {
		return value
	}

	switch v := value.(type) {
	case nil:
		return value
	case string:
		if v == "" {
			return value
		}
	case []string:
		if len(v) == 0 {
			return value
		}
	case []any:
		if len(v) == 0 {
			return value
		}
	}

	return Redacted
}

// EnvVar is the environment variable viper reads key from.
func EnvVar(key string) string {
	return "PHENIX_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(key))
}

// Source names where key's value comes from, in viper's order: a set flag,
// the PHENIX_ variable, the config files, then the default.
func Source(key string, flagChanged, inFile bool, lookupEnv func(string) (string, bool)) string {
	if flagChanged {
		return SourceFlag
	}

	if value, ok := lookupEnv(EnvVar(key)); ok && value != "" {
		return SourceEnv
	}

	if inFile {
		return SourceFile
	}

	return SourceDefault
}

// Setting is one runtime setting: its value, its default and where the value
// comes from.
type Setting struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
	// Default is the text of the default of the setting's flag, or nil for a
	// key that no flag declares.
	Default any    `json:"default"`
	Source  string `json:"source"`
	// Restart is true when a change takes effect only after phenix ui restarts.
	Restart bool `json:"restart"`
}

// Change is what Set or Unset did.
type Change struct {
	Key string `json:"key"`
	// File is the config file changed, or that did not have the key.
	File string `json:"file"`
	// Changed is false when Unset found no key in the file to remove.
	Changed bool `json:"changed"`
	// Restart is true when the change takes effect only after phenix ui
	// restarts.
	Restart bool `json:"restart"`
}

// Settings reads the runtime settings of a viper and changes them in its
// config file. Its changes are made one at a time.
type Settings struct {
	v         *viper.Viper
	flag      func(key string) *pflag.Flag
	file      func() string
	lookupEnv func(string) (string, bool)

	mu sync.Mutex
}

// UnsetOption changes what Settings.Unset does.
type UnsetOption func(*unsetOptions)

type unsetOptions struct {
	dryRun bool
}

// UnsetDryRun makes Settings.Unset check the key and report the change, and
// change nothing.
func UnsetDryRun() UnsetOption {
	return func(o *unsetOptions) { o.dryRun = true }
}

// Option configures a Settings.
type Option func(*Settings)

// WithFlags has a Settings find the flag of a setting key with flag, which
// returns nil for a key that no flag declares.
func WithFlags(flag func(key string) *pflag.Flag) Option {
	return func(s *Settings) { s.flag = flag }
}

// WithFile has a Settings change the config file that file names.
func WithFile(file func() string) Option {
	return func(s *Settings) { s.file = file }
}

// WithLookupEnv has a Settings read the environment with lookup.
func WithLookupEnv(lookup func(string) (string, bool)) Option {
	return func(s *Settings) { s.lookupEnv = lookup }
}

// New returns the Settings of v. Without options, it knows no flags, reads
// the process environment, and changes the config file v read.
func New(v *viper.Viper, opts ...Option) *Settings {
	s := &Settings{
		v:         v,
		flag:      func(string) *pflag.Flag { return nil },
		file:      v.ConfigFileUsed,
		lookupEnv: os.LookupEnv,
		mu:        sync.Mutex{},
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// File returns the config file that Set and Unset change, or "" when there is
// none, such as for an empty viper that read no file.
func (s *Settings) File() string {
	return s.file()
}

// List returns every setting that has a value or a flag, sorted by key, with
// the values of secret settings redacted.
func (s *Settings) List() []Setting {
	keys := s.v.AllKeys()
	slices.Sort(keys)

	list := make([]Setting, 0, len(keys))

	inFile := s.fileKeys()

	for _, key := range slices.Compact(keys) {
		list = append(list, s.setting(key, inFile))
	}

	return list
}

// fileKeys are the dotted keys of the config file that the viper read.
func (s *Settings) fileKeys() map[string]bool {
	keys := make(map[string]bool)

	used := s.v.ConfigFileUsed()
	if used == "" {
		return keys
	}

	file := viper.New()
	file.SetConfigFile(used)

	if err := file.ReadInConfig(); err != nil {
		return keys
	}

	for _, key := range file.AllKeys() {
		keys[key] = true
	}

	return keys
}

// Get returns the setting key with its value redacted if it is a secret, or
// an error that wraps ErrNotFound for a key that has no value and no flag.
func (s *Settings) Get(key string) (Setting, error) {
	key = strings.ToLower(key)

	if s.v.Get(key) == nil && s.flag(key) == nil {
		return Setting{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}

	return s.setting(key, s.fileKeys()), nil
}

// setting is the setting key; inFile holds the keys of the config file.
func (s *Settings) setting(key string, inFile map[string]bool) Setting {
	f := s.flag(key)

	var def any
	if f != nil {
		def = Redact(key, f.DefValue)
	}

	return Setting{
		Key:     key,
		Value:   Redact(key, s.v.Get(key)),
		Default: def,
		Source:  Source(key, f != nil && f.Changed, inFile[key] || s.v.InConfig(key), s.lookupEnv),
		Restart: !Live(key),
	}
}

// Set writes text at key in the config file, parsed as the flag of the
// setting does (see ParseValue), as phenix settings set does. Text that the
// flag refuses returns an error that wraps ErrSettingType, and a config file
// that is not YAML one that wraps ErrNotYAML.
func (s *Settings) Set(key, text string) (Change, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	file := s.file()

	value, err := ParseValue(s.flag(key), key, text)
	if err != nil {
		return Change{}, err
	}

	if err := SetInFile(file, key, value); err != nil {
		return Change{}, err
	}

	return Change{Key: key, File: file, Changed: true, Restart: !Live(key)}, nil
}

// Unset removes key from the config file, as phenix settings unset does, so a
// PHENIX_ variable or the default applies. A file without the key does not
// change. A config file that does not exist returns ErrNoConfigFile. With
// UnsetDryRun, it returns the change that it would make, and changes nothing.
func (s *Settings) Unset(key string, opts ...UnsetOption) (Change, error) {
	var o unsetOptions
	for _, opt := range opts {
		opt(&o)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	file := s.file()

	if _, err := os.Stat(file); os.IsNotExist(err) {
		return Change{}, ErrNoConfigFile
	}

	unset := UnsetInFile
	if o.dryRun {
		unset = HasInFile
	}

	changed, err := unset(file, key)
	if err != nil {
		return Change{}, err
	}

	return Change{Key: key, File: file, Changed: changed, Restart: !Live(key)}, nil
}
