package contracts

import (
	_ "embed"
	"encoding/json"
	"path/filepath"
)

//go:embed platform-layout.json
var platformLayoutJSON []byte

type platformLayout struct {
	Schema                  string              `json:"schema"`
	CurrentLink             string              `json:"currentLink"`
	VersionRoots            layoutDirectory     `json:"versionRoots"`
	SharedStateDirectory    string              `json:"sharedStateDirectory"`
	InstallerStateDirectory string              `json:"installerStateDirectory"`
	HostStateDirectory      string              `json:"hostStateDirectory"`
	PlatformDataRoot        PlatformDataRoot    `json:"platformDataRoot"`
	OwnershipRecord         string              `json:"ownershipRecord"`
	OwnedEntries            []string            `json:"ownedEntries"`
	SharedRuntimePaths      []SharedRuntimePath `json:"sharedRuntimePaths"`
	VersionStatePaths       []string            `json:"versionStatePaths"`
	RuntimeStatePaths       []string            `json:"runtimeStatePaths"`
	InstallerRecords        installerRecords    `json:"installerRecords"`
}

type installerRecords struct {
	OperationLogs     string `json:"operationLogs"`
	FailureDossiers   string `json:"failureDossiers"`
	Snapshots         string `json:"snapshots"`
	SnapshotRetention int    `json:"snapshotRetention"`
}

type layoutDirectory struct {
	Path string `json:"path"`
}

type SharedRuntimePath struct {
	PlatformPath string `json:"platformPath"`
	StatePath    string `json:"statePath"`
	Kind         string `json:"kind"`
	Preinstall   bool   `json:"preinstall,omitempty"`
}

type PlatformDataRoot struct {
	PlatformPath string `json:"platformPath"`
	StatePath    string `json:"statePath"`
}

// VersionRootsDirectory derives the version directory inside the user's
// installation container from the embedded layout contract.
func VersionRootsDirectory(installRoot string) string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil {
		panic("invalid embedded platform layout contract: " + err.Error())
	}
	if layout.VersionRoots.Path == "" {
		panic("invalid embedded platform layout version-root path")
	}
	return filepath.Join(installRoot, filepath.FromSlash(layout.VersionRoots.Path))
}

func CurrentPlatformRoot(installRoot string) string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil || layout.CurrentLink == "" {
		panic("invalid embedded platform layout current-link path")
	}
	return filepath.Join(installRoot, filepath.FromSlash(layout.CurrentLink))
}

func SharedStateDirectory(installRoot string) string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil || layout.SharedStateDirectory == "" {
		panic("invalid embedded platform layout shared-state path")
	}
	return filepath.Join(installRoot, filepath.FromSlash(layout.SharedStateDirectory))
}

func InstallerStateDirectory(installRoot string) string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil || layout.InstallerStateDirectory == "" {
		panic("invalid embedded platform layout installer-state path")
	}
	return filepath.Join(installRoot, filepath.FromSlash(layout.InstallerStateDirectory))
}

func OwnershipRecordPath(installRoot string) string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil || layout.OwnershipRecord == "" {
		panic("invalid embedded platform layout ownership-record path")
	}
	return filepath.Join(installRoot, filepath.FromSlash(layout.OwnershipRecord))
}

func HostStateDirectory(installRoot string) string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil || layout.HostStateDirectory == "" {
		panic("invalid embedded platform layout host-state path")
	}
	return filepath.Join(installRoot, filepath.FromSlash(layout.HostStateDirectory))
}

func InstallerRecordPath(installRoot, record string) string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil {
		panic("invalid embedded platform layout contract: " + err.Error())
	}
	var relative string
	switch record {
	case "operationLogs":
		relative = layout.InstallerRecords.OperationLogs
	case "failureDossiers":
		relative = layout.InstallerRecords.FailureDossiers
	case "snapshots":
		relative = layout.InstallerRecords.Snapshots
	default:
		panic("invalid embedded platform layout installer-record key")
	}
	if relative == "" {
		panic("invalid embedded platform layout installer-record path")
	}
	return filepath.Join(installRoot, filepath.FromSlash(relative))
}

func SnapshotRetention() int {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil || layout.InstallerRecords.SnapshotRetention < 0 {
		panic("invalid embedded platform layout snapshot retention")
	}
	return layout.InstallerRecords.SnapshotRetention
}

func OwnedInstallEntries() []string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil {
		panic("invalid embedded platform layout contract: " + err.Error())
	}
	return append([]string(nil), layout.OwnedEntries...)
}

func SharedRuntimePaths() []SharedRuntimePath {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil {
		panic("invalid embedded platform layout contract: " + err.Error())
	}
	return append([]SharedRuntimePath(nil), layout.SharedRuntimePaths...)
}

func PlatformDataRootPath() PlatformDataRoot {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil ||
		layout.PlatformDataRoot.PlatformPath == "" || layout.PlatformDataRoot.StatePath == "" {
		panic("invalid embedded platform layout data root")
	}
	return layout.PlatformDataRoot
}

// SharedRuntimeProjections returns the layout-owned mutable paths linked into
// each version root, including the single first-class platform data root.
func SharedRuntimeProjections() []SharedRuntimePath {
	dataRoot := PlatformDataRootPath()
	projections := []SharedRuntimePath{{
		PlatformPath: dataRoot.PlatformPath,
		StatePath:    dataRoot.StatePath,
		Kind:         "directory",
	}}
	return append(projections, SharedRuntimePaths()...)
}

func PlatformLayoutDocument() []byte {
	return append([]byte(nil), platformLayoutJSON...)
}

// RuntimeStatePaths returns the root-relative paths that a running host may
// mutate without changing the installed platform files.
func RuntimeStatePaths() []string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil {
		panic("invalid embedded platform layout contract: " + err.Error())
	}
	return append([]string(nil), layout.RuntimeStatePaths...)
}

// VersionStatePaths returns paths whose contents describe or belong to one
// sealed version and therefore stay inside its version root.
func VersionStatePaths() []string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil {
		panic("invalid embedded platform layout contract: " + err.Error())
	}
	return append([]string(nil), layout.VersionStatePaths...)
}
