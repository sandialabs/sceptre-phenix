package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"phenix/store"
)

const (
	BuilderXMLAnnotation     = "builder-xml"
	BuilderXMLFileAnnotation = "builder-xml-file"
)

// HasBuilderXML reports whether a config references embedded or file-backed Builder XML.
func HasBuilderXML(c store.Config) bool {
	return c.HasAnnotation(BuilderXMLAnnotation) || c.HasAnnotation(BuilderXMLFileAnnotation)
}

// BuilderXML loads the Builder diagram associated with a config.
func BuilderXML(c store.Config) ([]byte, error) {
	xml, embedded := c.Metadata.Annotations[BuilderXMLAnnotation]
	path, fromFile := c.Metadata.Annotations[BuilderXMLFileAnnotation]

	if embedded && fromFile {
		return nil, fmt.Errorf(
			"annotations %q and %q are mutually exclusive",
			BuilderXMLAnnotation,
			BuilderXMLFileAnnotation,
		)
	}

	if embedded {
		return []byte(xml), nil
	}

	if !fromFile {
		return nil, errors.New("builder XML annotation missing")
	}

	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("annotation %q cannot be empty", BuilderXMLFileAnnotation)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading builder XML file %q: %w", path, err)
	}

	return data, nil
}

// WriteBuilderXMLFile replaces the diagram for a file-backed Builder config.
func WriteBuilderXMLFile(c store.Config, data []byte) error {
	if c.HasAnnotation(BuilderXMLAnnotation) {
		return fmt.Errorf(
			"annotation %q cannot be used with %q",
			BuilderXMLAnnotation,
			BuilderXMLFileAnnotation,
		)
	}

	path, ok := c.Metadata.Annotations[BuilderXMLFileAnnotation]
	if !ok || strings.TrimSpace(path) == "" {
		return fmt.Errorf("annotation %q missing or empty", BuilderXMLFileAnnotation)
	}

	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolving builder XML file %q: %w", path, err)
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return fmt.Errorf("statting builder XML file %q: %w", path, err)
	}

	temp, err := os.CreateTemp(
		filepath.Dir(resolvedPath),
		"."+filepath.Base(resolvedPath)+".*",
	)
	if err != nil {
		return fmt.Errorf("creating temporary builder XML file: %w", err)
	}

	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		_ = temp.Close()

		return fmt.Errorf("setting builder XML file permissions: %w", err)
	}

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()

		return fmt.Errorf("writing builder XML file: %w", err)
	}

	if err := temp.Sync(); err != nil {
		_ = temp.Close()

		return fmt.Errorf("syncing builder XML file: %w", err)
	}

	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing builder XML file: %w", err)
	}

	if err := os.Rename(tempPath, resolvedPath); err != nil {
		return fmt.Errorf("replacing builder XML file %q: %w", path, err)
	}

	return nil
}

func normalizeBuilderXMLFilePath(c *store.Config, source string) error {
	if c.Kind != "Topology" || c.Metadata.Annotations == nil {
		return nil
	}

	path, ok := c.Metadata.Annotations[BuilderXMLFileAnnotation]
	if !ok || strings.TrimSpace(path) == "" || filepath.IsAbs(path) {
		return nil
	}

	base := "."
	if source != "" {
		base = filepath.Dir(source)
	}

	path, err := filepath.Abs(filepath.Join(base, path))
	if err != nil {
		return fmt.Errorf("resolving builder XML file %q: %w", path, err)
	}

	c.Metadata.Annotations[BuilderXMLFileAnnotation] = filepath.Clean(path)

	return nil
}

func validateBuilderAnnotations(stage string, c *store.Config) error {
	if c.Kind != "Topology" || (stage != "create" && stage != "update" && stage != "startup") {
		return nil
	}

	if !HasBuilderXML(*c) {
		return nil
	}

	if _, err := BuilderXML(*c); err != nil {
		return fmt.Errorf("loading builder XML: %w", err)
	}

	return nil
}

func init() { //nolint:gochecknoinits // config hook
	RegisterConfigHook("Topology", validateBuilderAnnotations)
}
