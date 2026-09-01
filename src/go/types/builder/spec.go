package builder

import (
	"fmt"
	"math"
	"strings"
)

// specNodeInterfaces returns the interface list of a node spec, if present.
func specNodeInterfaces(spec map[string]any) []any {
	network, ok := spec["network"].(map[string]any)
	if !ok {
		return nil
	}

	ifaces, ok := network["interfaces"].([]any)
	if !ok {
		return nil
	}

	return ifaces
}

// specString reads a string value from nested spec maps, tolerating missing
// keys and non-string values.
func specString(spec map[string]any, keys ...string) string {
	current := any(spec)

	for _, key := range keys {
		asMap, ok := current.(map[string]any)
		if !ok {
			return ""
		}

		current, ok = asMap[key]
		if !ok {
			return ""
		}
	}

	value, _ := current.(string)

	return value
}

// specSetString sets a string value in nested spec maps, creating intermediate
// maps as needed. It reports false when an existing intermediate value is not a
// map (in which case nothing is modified).
func specSetString(spec map[string]any, value string, keys ...string) bool {
	if len(keys) == 0 {
		return false
	}

	current := spec

	for _, key := range keys[:len(keys)-1] {
		next, ok := current[key]
		if !ok || next == nil {
			created := map[string]any{}
			current[key] = created
			current = created

			continue
		}

		asMap, ok := next.(map[string]any)
		if !ok {
			return false
		}

		current = asMap
	}

	current[keys[len(keys)-1]] = value

	return true
}

// normalizeSpec converts an arbitrary decoded value (JSON or YAML) into a
// canonical map[string]any / []any / scalar tree. YAML decoders may produce
// map[any]any and JSON decoders produce float64 for every number; both are
// normalized so specs survive a round trip through either format.
func normalizeSpec(value any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))

		for key, val := range typed {
			normalized, err := normalizeSpec(val)
			if err != nil {
				return nil, err
			}

			out[key] = normalized
		}

		return out, nil
	case map[any]any:
		out := make(map[string]any, len(typed))

		for key, val := range typed {
			asString, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("non-string spec key %v (%T)", key, key)
			}

			normalized, err := normalizeSpec(val)
			if err != nil {
				return nil, err
			}

			out[asString] = normalized
		}

		return out, nil
	case []any:
		out := make([]any, len(typed))

		for i, val := range typed {
			normalized, err := normalizeSpec(val)
			if err != nil {
				return nil, err
			}

			out[i] = normalized
		}

		return out, nil
	case float64:
		// JSON decodes every number as float64; integral values are restored to
		// int so generated specs marshal (and schema validate) as integers.
		if typed == math.Trunc(typed) && !math.IsInf(typed, 0) &&
			math.Abs(typed) <= math.MaxInt32 {
			return int(typed), nil
		}

		return typed, nil
	case float32:
		return normalizeSpec(float64(typed))
	case int64:
		return int(typed), nil
	case int32:
		return int(typed), nil
	default:
		return value, nil
	}
}

// normalizeSpecMap normalizes a spec map, returning a deep copy.
func normalizeSpecMap(value any) (map[string]any, error) {
	normalized, err := normalizeSpec(value)
	if err != nil {
		return nil, err
	}

	if normalized == nil {
		return nil, nil //nolint:nilnil // absent spec is not an error
	}

	asMap, ok := normalized.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected an object, got %T", value)
	}

	return asMap, nil
}

// interfaceName reads the interface name from a spec interface entry.
func interfaceName(iface any) string {
	asMap, ok := iface.(map[string]any)
	if !ok {
		return ""
	}

	name, _ := asMap["name"].(string)

	return name
}

// interfaceVLAN reads the VLAN name from a spec interface entry.
func interfaceVLAN(iface any) string {
	asMap, ok := iface.(map[string]any)
	if !ok {
		return ""
	}

	vlan, _ := asMap["vlan"].(string)

	return vlan
}

// foldKey normalizes a value for case-insensitive comparisons.
func foldKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
