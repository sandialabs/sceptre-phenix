package experiment

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	ifaces "phenix/types/interfaces"
)

// validateCreateAnnotations checks the experiment and node annotations a new
// experiment is given.
func validateCreateAnnotations(annotations map[string]string, nodeAnnotations map[string]any) error {
	if err := validateAnnotations(annotations); err != nil {
		return err
	}

	return ValidateNodeAnnotations(nodeAnnotations)
}

// validateAnnotations rejects experiment annotations that phenix sets itself
// from the experiment's topology and scenario.
func validateAnnotations(annotations map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(annotations)) {
		switch {
		case strings.TrimSpace(key) == "":
			return errors.New("experiment annotation keys cannot be empty")
		case key == "topology", key == "scenario":
			return fmt.Errorf("experiment annotation %q is set by phenix and cannot be given", key)
		}
	}

	return nil
}

// ValidateNodeAnnotations rejects empty keys, null values, and values of the
// wrong type for the annotations the default apps read.
func ValidateNodeAnnotations(annotations map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(annotations)) {
		value := annotations[key]

		switch {
		case strings.TrimSpace(key) == "":
			return errors.New("node annotation keys cannot be empty")
		case value == nil:
			return fmt.Errorf("node annotation %q has no value", key)
		}

		if err := validateDefaultAppAnnotation(key, value); err != nil {
			return err
		}
	}

	return nil
}

func validateDefaultAppAnnotation(key string, value any) error {
	switch key {
	case "phenix/default-apps":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("node annotation %q must be true or false", key)
		}
	case "phenix/startup-autotunnel":
		if !isStringList(value) {
			return fmt.Errorf(
				"node annotation %q must be a list of port forwards, such as [\"8080:80\"]",
				key,
			)
		}
	case "vrouter/vyos-password", "vrouter/enable-ssh":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("node annotation %q must be a string", key)
		}
	}

	return nil
}

func isStringList(value any) bool {
	switch list := value.(type) {
	case []string:
		return true
	case []any:
		for _, item := range list {
			if _, ok := item.(string); !ok {
				return false
			}
		}

		return true
	default:
		return false
	}
}

// applyNodeAnnotations adds the annotations to every VM in the topology,
// keeping the value a VM already has for the same key.
func applyNodeAnnotations(topo ifaces.TopologySpec, annotations map[string]any) {
	for _, node := range topo.Nodes() {
		// external nodes are not VMs phenix deploys
		if node.External() {
			continue
		}

		for key, value := range annotations {
			if _, ok := node.GetAnnotation(key); !ok {
				node.AddAnnotation(key, value)
			}
		}
	}
}
