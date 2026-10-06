package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// LoadFile reads the immutable release index selected before any artifact
// install. It is deliberately a file input: registry discovery belongs to the
// release publisher, while CI/agents need a reviewable exact index.
func LoadFile(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, wrapIndexError(path, "read", "readable release index", err.Error(), err)
	}
	return decodeFile(data, path)
}

// Decode parses and verifies a release index. The schema gate runs first, so
// an index of an unsupported shape is rejected before anything else reads it.
func Decode(data []byte) (Catalog, error) {
	return decodeFile(data, "<memory>")
}

func decodeFile(data []byte, path string) (Catalog, error) {
	schema, err := peekSchema(data)
	if err != nil {
		return Catalog{}, wrapIndexError(path, "parse", "valid release-index JSON", err.Error(), err)
	}
	if err := CheckSchema(schema); err != nil {
		actualSchema := schema
		if actualSchema == "" {
			actualSchema = "<missing>"
		}
		return Catalog{}, wrapIndexError(path, "schema version", strings.Join(SupportedSchemas, ", "), actualSchema, err)
	}
	var result Catalog
	if err := json.Unmarshal(data, &result); err != nil {
		return Catalog{}, wrapIndexError(path, "parse", "release-index JSON matching the schema", err.Error(), err)
	}
	if err := Verify(result); err != nil {
		var failure *indexGateError
		if errors.As(err, &failure) {
			return Catalog{}, wrapIndexError(path, failure.gate, failure.expected, failure.actual, failure.err)
		}
		return Catalog{}, wrapIndexError(path, "validation rule", "valid release index", err.Error(), err)
	}
	if result.Compatibility == nil {
		err := fmt.Errorf("compatibility matrix is required")
		return Catalog{}, wrapIndexError(path, "validation rule", "compatibility matrix", "missing", err)
	}
	return result, nil
}
