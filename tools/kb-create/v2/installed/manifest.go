// Package installed reads and materializes the exact V2 metadata selected by
// the sealed release index. Doctor uses it after installation; the launcher
// never guesses configuration ownership or scans unrelated packages.
package installed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/doctor"
	"github.com/kb-labs/create/v2/secrets"
)

const ManifestSchema = "kb.create.artifact-manifest/v2"

type Manifest struct {
	Schema       string               `json:"schema"`
	ID           string               `json:"id"`
	Package      string               `json:"package"`
	Version      string               `json:"version"`
	Requirements []doctor.Requirement `json:"requirements,omitempty"`
}

// DoctorInput loads requirements only from the exact packages recorded in the
// active receipt. Missing or mismatched metadata is a release integrity error,
// not a reason to silently diagnose an incomplete system.
func DoctorInput(platformRoot string, artifacts []contracts.Artifact, configured map[string]bool) (doctor.Input, error) {
	result := doctor.Input{Configured: configured}
	manifests, err := LoadAll(platformRoot, artifacts)
	if err != nil {
		return doctor.Input{}, err
	}
	for _, manifest := range manifests {
		result.Manifests = append(result.Manifests, doctor.Manifest{ID: manifest.ID, Requirements: manifest.Requirements})
	}
	store := secrets.Store{PlatformRoot: platformRoot}
	for _, manifest := range result.Manifests {
		for _, requirement := range manifest.Requirements {
			if !requirement.Secret {
				continue
			}
			exists, err := store.Exists(requirement.ID)
			if err != nil {
				return doctor.Input{}, fmt.Errorf("check configured secret %s: %w", requirement.ID, err)
			}
			result.Configured[requirement.ID] = exists || configured[requirement.Path] || (requirement.Env != "" && os.Getenv(requirement.Env) != "")
		}
	}
	return result, nil
}

func LoadAll(platformRoot string, artifacts []contracts.Artifact) ([]Manifest, error) {
	result := make([]Manifest, 0, len(artifacts))
	for _, artifact := range artifacts {
		manifest, err := Load(platformRoot, artifact)
		if err != nil {
			return nil, err
		}
		result = append(result, manifest)
	}
	return result, nil
}

// Write materializes the exact artifact metadata carried by a resolved plan
// beside its installed package. Release tarballs need not duplicate this
// projection: the sealed index is the source and the installer writes it only
// after the exact package has been selected.
func Write(platformRoot string, artifact contracts.Artifact) error {
	if artifact.ID == "" || artifact.Package == "" || artifact.Version == "" {
		return fmt.Errorf("artifact lacks exact ID/package/version for manifest materialization")
	}
	manifest := Manifest{Schema: ManifestSchema, ID: artifact.ID, Package: artifact.Package, Version: artifact.Version}
	for _, requirement := range artifact.Config {
		manifest.Requirements = append(manifest.Requirements, doctor.Requirement{
			ID: requirement.ID, Path: requirement.Path, Required: requirement.Required,
			Secret: requirement.Secret, Generate: requirement.Generate, Hint: requirement.Hint, Env: requirement.Env,
			Services: append([]string(nil), requirement.Services...), Default: []byte(requirement.Default),
		})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode installed manifest for %s: %w", artifact.Package, err)
	}
	path := filepath.Join(platformRoot, "node_modules", filepath.FromSlash(artifact.Package), "kb-create.manifest.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("prepare installed manifest for %s: %w", artifact.Package, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write installed manifest for %s: %w", artifact.Package, err)
	}
	return nil
}

func Load(platformRoot string, artifact contracts.Artifact) (Manifest, error) {
	if artifact.Package == "" || artifact.Version == "" {
		return Manifest{}, fmt.Errorf("artifact %q lacks exact package/version for manifest verification", artifact.ID)
	}
	base := filepath.Join(platformRoot, "node_modules", filepath.FromSlash(artifact.Package))
	paths := []string{filepath.Join(base, "kb-create.manifest.json"), filepath.Join(base, "dist", "kb-create.manifest.json")}
	var data []byte
	var err error
	for _, path := range paths {
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return Manifest{}, fmt.Errorf("read installed manifest for %s: %w", artifact.Package, err)
		}
	}
	if data == nil {
		return Manifest{}, fmt.Errorf("installed artifact %s@%s does not ship %s", artifact.Package, artifact.Version, ManifestSchema)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode installed manifest for %s: %w", artifact.Package, err)
	}
	if manifest.Schema != ManifestSchema || manifest.ID != artifact.ID || manifest.Package != artifact.Package || manifest.Version != artifact.Version {
		return Manifest{}, fmt.Errorf("installed manifest does not match resolved artifact %s@%s", artifact.Package, artifact.Version)
	}
	for _, requirement := range manifest.Requirements {
		if requirement.ID == "" || (!requirement.Secret && requirement.Path == "") || (requirement.Secret && strings.TrimSpace(requirement.ID) == "") {
			return Manifest{}, fmt.Errorf("installed manifest %s has invalid configuration requirement", artifact.Package)
		}
	}
	return manifest, nil
}
