// Package nodes holds the built-in FlowForge node types. Each type registers
// itself against engine.DefaultRegistry in an init() function. See
// docs/engine-v1.md §11.
package nodes

import "fmt"

// stringConfig reads key from a node's config as a string. A missing key
// yields def, or an error if required is set; a present key of any other type
// is always an error, so typos in a flow surface at load time.
func stringConfig(config map[string]any, key string, required bool, def string) (string, error) {
	v, ok := config[key]
	if !ok || v == nil {
		if required {
			return "", fmt.Errorf("config %q is required", key)
		}
		return def, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("config %q must be a string, got %T", key, v)
	}
	if required && s == "" {
		return "", fmt.Errorf("config %q must not be empty", key)
	}
	return s, nil
}
