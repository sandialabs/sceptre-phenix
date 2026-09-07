package runtimeconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const keyParts = 2

func Set(configFile, key string, value any) error {
	if configFile == "" {
		return fmt.Errorf("runtime configuration file path is empty")
	}

	data, err := os.ReadFile(configFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading config file: %w", err)
	}

	config := make(map[string]any)
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parsing config file: %w", err)
		}
	}

	setNested(config, key, value)

	data, err = yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(configFile), 0o750); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	if err := os.WriteFile(configFile, data, 0o600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

func setNested(config map[string]any, key string, value any) {
	parts := strings.SplitN(key, ".", keyParts)
	target := parts[0]

	for existing := range config {
		if strings.EqualFold(existing, target) {
			target = existing
			break
		}
	}

	if len(parts) == 1 {
		config[target] = value
		return
	}

	next, ok := config[target].(map[string]any)
	if !ok {
		next = make(map[string]any)
		config[target] = next
	}

	setNested(next, parts[1], value)
}
