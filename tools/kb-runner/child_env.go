//go:build darwin || linux

package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

var childBaseEnvironment = []string{
	"HOME", "PATH", "USER", "LANG", "LC_ALL", "LC_COLLATE", "LC_CTYPE", "LC_MESSAGES", "LC_MONETARY", "LC_NUMERIC", "LC_TIME", "LC_ADDRESS", "LC_IDENTIFICATION", "LC_MEASUREMENT", "LC_NAME", "LC_PAPER", "LC_TELEPHONE", "TERM", "TMPDIR", "SHELL",
}

func childEnvironment(declared []string) ([]string, error) {
	values := make(map[string]string, len(childBaseEnvironment)+len(declared))
	for _, name := range childBaseEnvironment {
		if value, ok := os.LookupEnv(name); ok {
			values[name] = value
		}
	}
	for _, entry := range declared {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" || strings.Contains(name, "=") {
			return nil, fmt.Errorf("invalid vendor child environment declaration %q", name)
		}
		values[name] = value
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	env := make([]string, 0, len(names))
	for _, name := range names {
		env = append(env, name+"="+values[name])
	}
	return env, nil
}

func vendorChildEnvironment(vendor string) ([]string, error) {
	spec, ok := vendorRegistry[vendor]
	if !ok {
		return nil, fmt.Errorf("unsupported vendor %q", vendor)
	}
	return childEnvironment(spec.childEnv)
}

func vendorWorkerEnvironment(vendor string) ([]string, error) {
	spec, ok := vendorRegistry[vendor]
	if !ok {
		return nil, fmt.Errorf("unsupported vendor %q", vendor)
	}
	declared := append([]string(nil), spec.childEnv...)
	if binary := os.Getenv(spec.binaryEnv); binary != "" {
		declared = append(declared, spec.binaryEnv+"="+binary)
	}
	return childEnvironment(declared)
}
