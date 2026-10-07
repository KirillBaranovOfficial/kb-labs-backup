// Package gateway tracks installed host roots and their Gateway origins.
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const registrySchema = 1

type Root struct {
	PlatformRoot string `json:"platformRoot"`
	URL          string `json:"url"`
}

type file struct {
	Schema int    `json:"schema"`
	Roots  []Root `json:"roots"`
}

// Registry is the machine-level index of installed host roots. Each receipt is
// still authoritative; this index lets a fresh root find receipts belonging to
// roots whose paths are otherwise unknown to the launcher.
type Registry struct{ Path string }

func (r Registry) Read() ([]Root, error) {
	data, err := os.ReadFile(r.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read installed Gateway root index %s: %w", r.Path, err)
	}
	var value file
	if err := json.Unmarshal(data, &value); err != nil || value.Schema != registrySchema || value.Roots == nil {
		if err == nil {
			err = fmt.Errorf("unsupported or incomplete schema")
		}
		return nil, fmt.Errorf("installed Gateway root index %s is invalid: %w", r.Path, err)
	}
	seen := make(map[string]struct{}, len(value.Roots))
	for _, root := range value.Roots {
		if !filepath.IsAbs(root.PlatformRoot) || !validOrigin(root.URL) {
			return nil, fmt.Errorf("installed Gateway root index %s contains an invalid root or URL", r.Path)
		}
		if _, exists := seen[root.PlatformRoot]; exists {
			return nil, fmt.Errorf("installed Gateway root index %s contains duplicate root %s", r.Path, root.PlatformRoot)
		}
		seen[root.PlatformRoot] = struct{}{}
	}
	return value.Roots, nil
}

func OriginFromHealthURL(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || !validOriginParts(parsed) {
		return "", fmt.Errorf("Gateway health URL %q has no valid loopback origin", value)
	}
	parsed.Path, parsed.RawPath, parsed.RawQuery, parsed.Fragment = "", "", "", ""
	return parsed.String(), nil
}

// Allocate keeps a root's registered address, honors an explicit URL, and
// otherwise advances in 100-port offsets until no installed root claims it.
func (r Registry) Allocate(platformRoot, defaultURL, explicitURL string) (string, error) {
	return r.AllocateWithKnown(platformRoot, defaultURL, explicitURL, nil)
}

func (r Registry) AllocateWithKnown(platformRoot, defaultURL, explicitURL string, known []Root) (string, error) {
	if explicitURL != "" {
		return explicitURL, nil
	}
	roots, err := r.Read()
	if err != nil {
		return "", err
	}
	roots = append(roots, known...)
	claimed := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		if filepath.Clean(root.PlatformRoot) == filepath.Clean(platformRoot) {
			return root.URL, nil
		}
		claimed[root.URL] = struct{}{}
	}
	parsed, err := url.Parse(defaultURL)
	if err != nil || !validOrigin(defaultURL) {
		return "", fmt.Errorf("release Gateway origin %q is invalid", defaultURL)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		return "", fmt.Errorf("release Gateway origin %q has no valid port", defaultURL)
	}
	for {
		candidate := *parsed
		candidate.Host = strings.TrimSuffix(parsed.Host, ":"+parsed.Port()) + ":" + strconv.Itoa(port)
		origin := candidate.String()
		if _, exists := claimed[origin]; !exists {
			return origin, nil
		}
		port += 100
		if port > 65535 {
			return "", fmt.Errorf("no Gateway port remains after release port %s", parsed.Port())
		}
	}
}

func (r Registry) Claimant(platformRoot, gatewayURL string) (Root, bool, error) {
	return r.ClaimantAmong(platformRoot, gatewayURL, nil)
}

func (r Registry) ClaimantAmong(platformRoot, gatewayURL string, known []Root) (Root, bool, error) {
	roots, err := r.Read()
	if err != nil {
		return Root{}, false, err
	}
	for _, root := range append(roots, known...) {
		if filepath.Clean(root.PlatformRoot) != filepath.Clean(platformRoot) && root.URL == gatewayURL {
			return root, true, nil
		}
	}
	return Root{}, false, nil
}

func (r Registry) RegisterMany(values []Root) error {
	roots, err := r.Read()
	if err != nil {
		return err
	}
	byRoot := make(map[string]Root, len(roots)+len(values))
	for _, root := range roots {
		byRoot[filepath.Clean(root.PlatformRoot)] = root
	}
	for _, root := range values {
		if !filepath.IsAbs(root.PlatformRoot) || !validOrigin(root.URL) {
			return fmt.Errorf("platform root and Gateway URL must be absolute and valid")
		}
		root.PlatformRoot = filepath.Clean(root.PlatformRoot)
		byRoot[root.PlatformRoot] = root
	}
	roots = roots[:0]
	for _, root := range byRoot {
		roots = append(roots, root)
	}
	return r.write(roots)
}

func (r Registry) Register(platformRoot, gatewayURL string) error {
	if !filepath.IsAbs(platformRoot) || !validOrigin(gatewayURL) {
		return fmt.Errorf("platform root and Gateway URL must be absolute and valid")
	}
	roots, err := r.Read()
	if err != nil {
		return err
	}
	root := filepath.Clean(platformRoot)
	updated := false
	for index := range roots {
		if filepath.Clean(roots[index].PlatformRoot) == root {
			roots[index].URL = gatewayURL
			updated = true
			break
		}
	}
	if !updated {
		roots = append(roots, Root{PlatformRoot: root, URL: gatewayURL})
	}
	return r.write(roots)
}

func (r Registry) Remove(platformRoot string) error {
	roots, err := r.Read()
	if err != nil {
		return err
	}
	root := filepath.Clean(platformRoot)
	filtered := roots[:0]
	for _, entry := range roots {
		if filepath.Clean(entry.PlatformRoot) != root {
			filtered = append(filtered, entry)
		}
	}
	return r.write(filtered)
}

func (r Registry) write(roots []Root) error {
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o700); err != nil {
		return fmt.Errorf("create installed Gateway root index directory: %w", err)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].PlatformRoot < roots[j].PlatformRoot })
	if roots == nil {
		roots = []Root{}
	}
	data, err := json.MarshalIndent(file{Schema: registrySchema, Roots: roots}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(r.Path), ".gateway-roots-*.tmp")
	if err != nil {
		return fmt.Errorf("stage installed Gateway root index: %w", err)
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("protect installed Gateway root index: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write installed Gateway root index: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync installed Gateway root index: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close installed Gateway root index: %w", err)
	}
	if err := os.Rename(name, r.Path); err != nil {
		return fmt.Errorf("commit installed Gateway root index: %w", err)
	}
	return nil
}

func validOrigin(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && validOriginParts(parsed) && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

func validOriginParts(parsed *url.URL) bool {
	return parsed != nil && parsed.Scheme == "http" && parsed.User == nil && parsed.Hostname() == "127.0.0.1" && parsed.Port() != ""
}
