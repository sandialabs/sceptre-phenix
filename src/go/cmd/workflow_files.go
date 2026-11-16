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
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"phenix/util/envsubst"
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

// configPaths returns the path of every config file under dir. Hidden files
// and directories are skipped, and so are files that are not .json, .yaml or
// .yml files. A symlink to a regular file counts; a symlink to a directory is
// skipped with a warning.
func configPaths(dir string) ([]string, error) {
	var paths []string

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

		paths = append(paths, path)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return paths, nil
}

// loadWorkflowFiles reads the config files at paths and the workflow config,
// which is "" when there is none, fills in their variables in one pass, and
// parses them. It returns the configs in upsert order: Topology configs
// first, then the rest, each group sorted by path. Problems name each file by
// its path relative to dir.
func loadWorkflowFiles(dir string, paths []string, wfConfig string, vars *envsubst.Set) ([]configFile, *configFile, error) {
	files := make([]envsubst.File, 0, len(paths)+1)

	for _, path := range paths {
		body, err := readConfigFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("checking configs: %w", err)
		}

		files = append(files, envsubst.File{Path: relTo(dir, path), Text: body})
	}

	if wfConfig != "" {
		body, err := readConfigFile(wfConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("checking the workflow config: %w", err)
		}

		files = append(files, envsubst.File{Path: relTo(dir, wfConfig), Text: body})
	}

	files, err := substituteVars(vars, files, "step", stepPreflight)
	if err != nil {
		return nil, nil, err
	}

	configs := make([]configFile, 0, len(paths))

	for i, path := range paths {
		cfg, err := parseConfigBody(path, files[i].Text, true)
		if err != nil {
			return nil, nil, fmt.Errorf("checking configs: %w", err)
		}

		configs = append(configs, cfg)
	}

	slices.SortFunc(configs, compareConfigs)

	if wfConfig == "" {
		return configs, nil, nil
	}

	wf, err := parseConfigBody(wfConfig, files[len(paths)].Text, false)
	if err != nil {
		return nil, nil, fmt.Errorf("checking the workflow config: %w", err)
	}

	return configs, &wf, nil
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

// parseConfigBody checks that body, the text of path with its variables filled
// in, holds one YAML or JSON mapping, with the placeholders that stay masked,
// and returns it. requireKind marks a file from phenix-configs/, whose kind
// orders the upserts. The server checks the rest.
func parseConfigBody(path string, body []byte, requireKind bool) (configFile, error) {
	masked := neutralizePlaceholders(body)

	root, err := parseDocument(path, masked)
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

	cfg.Kind = unmaskedScalar(body, masked, kind)

	return cfg, nil
}

// unmaskedScalar returns the value of node, a scalar parsed from masked, as
// body writes it. A placeholder in a one-line scalar thus shows as written. For
// any other scalar it returns the masked value, because neutralizePlaceholders
// keeps each line and rune column, but not escapes or folded lines.
func unmaskedScalar(body, masked []byte, node *yaml.Node) string {
	start := node.Column - 1
	if node.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) != 0 {
		start++
	}

	end := start + utf8.RuneCountInString(node.Value)
	maskedLine, bodyLine := lineRunes(masked, node.Line), lineRunes(body, node.Line)

	if start < 0 || end > len(maskedLine) || end > len(bodyLine) || string(maskedLine[start:end]) != node.Value {
		return node.Value
	}

	return string(bodyLine[start:end])
}

// lineRunes returns the runes of line n of body, counted from 1.
func lineRunes(body []byte, n int) []rune {
	lines := bytes.Split(body, []byte("\n"))
	if n < 1 || n > len(lines) {
		return nil
	}

	return []rune(string(lines[n-1]))
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

// neutralizePlaceholders replaces each non-whitespace character of every
// placeholder with x. A placeholder then breaks no syntax check, and every
// line and column stays where it was.
func neutralizePlaceholders(body []byte) []byte {
	return envsubst.Replace(body, func(tok envsubst.Token) ([]byte, bool) {
		return bytes.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return r
			}

			return 'x'
		}, []byte(tok.Raw)), true
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
