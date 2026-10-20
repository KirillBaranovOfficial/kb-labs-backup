package installed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfiguredPaths reports JSON-pointer presence from V2's generated runtime
// config. It returns presence only: doctor never receives configuration values.
func ConfiguredPaths(platformRoot string, paths []string) (map[string]bool, error) {
	data, err := os.ReadFile(filepath.Join(platformRoot, ".kb", "kb.config.jsonc"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("read generated runtime config: %w", err)
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("decode generated runtime config: %w", err)
	}
	result := make(map[string]bool, len(paths))
	for _, path := range paths {
		result[path] = pointerConfigured(root, path)
	}
	return result, nil
}

func pointerConfigured(value any, pointer string) bool {
	if pointer == "" || pointer == "/" || !strings.HasPrefix(pointer, "/") {
		return false
	}
	current := value
	for _, segment := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = object[key]
		if !ok {
			return false
		}
	}
	if reference, ok := current.(string); ok && len(reference) > 3 && strings.HasPrefix(reference, "${") && strings.HasSuffix(reference, "}") {
		value, exists := os.LookupEnv(reference[2 : len(reference)-1])
		return exists && value != ""
	}
	if current == nil || current == "" {
		return false
	}
	return true
}
