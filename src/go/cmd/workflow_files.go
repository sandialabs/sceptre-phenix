package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"phenix/util/common"
	"phenix/util/plog"
)

const (
	mimeJSON     = "application/json"
	mimeYAML     = "application/x-yaml"
	kindTopology = "Topology"
	yamlPairLen  = 2
)

// contentTypeFor returns the request Content-Type for a config file: JSON for
// a .json extension and YAML for anything else.
func contentTypeFor(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return mimeJSON
	}

	return mimeYAML
}

// isConfigFile reports whether path ends in .json, .yaml or .yml, in any
// case: the extensions phenix config create reads.
func isConfigFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

// loadConfigs parses every config file under dir and returns them in upsert
// order: Topology configs first, then the rest, each group sorted by path.
// Hidden files and directories are skipped, and so are files that are not
// .json, .yaml or .yml files. A symlink to a regular file is read; a symlink
// to a directory is skipped with a warning.
func loadConfigs(dir string) ([]configFile, error) {
	var files []configFile

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		hidden := path != dir && strings.HasPrefix(entry.Name(), ".")

		switch {
		case entry.IsDir() && hidden:
			return filepath.SkipDir
		case entry.IsDir(), hidden:
			return nil
		case entry.Type()&fs.ModeSymlink != 0 && existingDir(path) != "":
			// The walk does not follow a symlinked directory.
			plog.Warn(
				plog.TypeSystem, "skipping a symlinked directory; its configs are not deployed",
				"step", stepPreflight, "file", path,
			)

			return nil
		case !isConfigFile(path):
			plog.Debug(plog.TypeSystem, "skipping a file that is not a config", "step", stepPreflight, "file", path)

			return nil
		}

		file, err := parseConfigFile(path, true)
		if err != nil {
			return err
		}

		files = append(files, file)

		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.SortFunc(files, compareConfigs)

	return files, nil
}

// compareConfigs orders Topology configs before everything else, and each
// group by path.
func compareConfigs(a, b configFile) int {
	aTopology := strings.EqualFold(a.Kind, kindTopology)
	bTopology := strings.EqualFold(b.Kind, kindTopology)

	switch {
	case aTopology && !bTopology:
		return -1
	case bTopology && !aTopology:
		return 1
	default:
		return strings.Compare(a.Path, b.Path)
	}
}

// readConfigFile reads path into memory. It refuses anything that is not a
// regular file, following symlinks, so that a symlink to a device or a FIFO
// cannot make the command read without end.
func readConfigFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", path)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	return body, nil
}

// parseConfigFile reads path and checks that it holds exactly one YAML or
// JSON document with a mapping at the top level. The check parses a copy with
// the placeholders masked, since the server fills them in first; Body keeps
// the file as read. Kind is the document's kind, which must be a non-empty
// string when present. requireKind marks a file from phenix-configs/, whose
// kind orders the upserts. Everything else in a config is for the phenix
// server to check.
func parseConfigFile(path string, requireKind bool) (configFile, error) {
	body, err := readConfigFile(path)
	if err != nil {
		return configFile{}, err
	}

	root, err := parseDocument(path, neutralizePlaceholders(body))
	if err != nil {
		return configFile{}, err
	}

	if root.Kind != yaml.MappingNode {
		return configFile{}, fmt.Errorf("%s:%d:%d: expected a mapping at the top level", path, root.Line, root.Column)
	}

	var kind *yaml.Node

	for i := 0; i+1 < len(root.Content); i += yamlPairLen {
		if root.Content[i].Value == "kind" {
			kind = root.Content[i+1]

			break
		}
	}

	cfg := configFile{Path: path, Kind: "", ContentType: contentTypeFor(path), Body: body}

	if kind == nil {
		if requireKind {
			return configFile{}, fmt.Errorf("%s:%d:%d: missing kind", path, root.Line, root.Column)
		}

		return cfg, nil
	}

	if kind.Kind != yaml.ScalarNode || kind.Value == "" {
		return configFile{}, fmt.Errorf("%s:%d:%d: kind must be a non-empty string", path, kind.Line, kind.Column)
	}

	cfg.Kind = kind.Value

	return cfg, nil
}

// parseDocument parses body as a single YAML document and returns its root
// node. The server reads only the first document of a body, so a second
// document is an error.
func parseDocument(path string, body []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(body))

	var doc yaml.Node

	if err := decoder.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: no YAML document", path)
		}

		return nil, yamlError(path, err)
	}

	var next yaml.Node

	err := decoder.Decode(&next)
	if err == nil {
		return nil, fmt.Errorf("%s:%d:%d: found a second YAML document; use one file per config", path, next.Line, next.Column)
	}

	if !errors.Is(err, io.EOF) {
		return nil, yamlError(path, err)
	}

	return doc.Content[0], nil
}

// neutralizePlaceholders replaces every character of each placeholder in
// body with an x, except whitespace. The placeholders are the ${VAR} and
// ${VAR:default} that the phenix server fills in before it parses a config,
// as common.EnvPlaceholder matches them. A placeholder in a JSON number, a
// flow mapping or a value with a colon then no longer breaks the syntax
// check, and every line and column stays where it was.
func neutralizePlaceholders(body []byte) []byte {
	return common.EnvPlaceholder.ReplaceAllFunc(body, func(match []byte) []byte {
		return bytes.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return r
			}

			return 'x'
		}, match)
	})
}

// yamlError rewrites a YAML syntax error such as "yaml: line 3: did not find
// expected key" as "path:3: did not find expected key".
func yamlError(path string, err error) error {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")

	if rest, ok := strings.CutPrefix(msg, "line "); ok {
		if line, detail, found := strings.Cut(rest, ": "); found {
			return fmt.Errorf("%s:%s: %s", path, line, detail)
		}
	}

	return fmt.Errorf("%s: %s", path, msg)
}
