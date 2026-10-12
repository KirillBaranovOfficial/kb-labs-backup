package contracts

import (
	_ "embed"
	"encoding/json"
	"path/filepath"
	"strings"
)

//go:embed platform-layout.json
var platformLayoutJSON []byte

type platformLayout struct {
	Schema                       string              `json:"schema"`
	CurrentLink                  string              `json:"currentLink"`
	VersionRoots                 layoutDirectory     `json:"versionRoots"`
	SharedStateDirectory         string              `json:"sharedStateDirectory"`
	InstallerStateDirectory      string              `json:"installerStateDirectory"`
	HostStateDirectory           string              `json:"hostStateDirectory"`
	PlatformDataRoot             PlatformDataRoot    `json:"platformDataRoot"`
	OwnershipRecord              string              `json:"ownershipRecord"`
	OwnershipRecordStagingPrefix string              `json:"ownershipRecordStagingPrefix"`
	OwnedEntries                 []string            `json:"ownedEntries"`
	SharedRuntimePaths           []SharedRuntimePath `json:"sharedRuntimePaths"`
	VersionStatePaths            []string            `json:"versionStatePaths"`
	RuntimeStatePaths            []string            `json:"runtimeStatePaths"`
	InstallerRecords             installerRecords    `json:"installerRecords"`
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

func OwnershipRecordStagingPrefix() string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil || layout.OwnershipRecordStagingPrefix == "" ||
		strings.ContainsAny(layout.OwnershipRecordStagingPrefix, `/\\`) {
		panic("invalid embedded platform layout ownership-record staging prefix")
	}
	return layout.OwnershipRecordStagingPrefix
}

func OwnershipRecordStagingPath(installRoot, installationID string) string {
	return filepath.Join(installRoot, OwnershipRecordStagingPrefix()+installationID)
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

func InstallerRecordDirectories(installRoot string) []string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil ||
		layout.InstallerRecords.OperationLogs == "" || layout.InstallerRecords.FailureDossiers == "" ||
		layout.InstallerRecords.Snapshots == "" {
		panic("invalid embedded platform layout installer-record directories")
	}
	return []string{
		filepath.Join(installRoot, filepath.FromSlash(layout.InstallerRecords.OperationLogs)),
		filepath.Join(installRoot, filepath.FromSlash(layout.InstallerRecords.FailureDossiers)),
		filepath.Join(installRoot, filepath.FromSlash(layout.InstallerRecords.Snapshots)),
	}
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

// OwnedInstallEntryKinds derives each owned install-root entry's required
// filesystem kind from the embedded layout contract.
func OwnedInstallEntryKinds() map[string]string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil {
		panic("invalid embedded platform layout contract: " + err.Error())
	}
	kinds := map[string]string{}
	add := func(path, kind string) {
		rootEntry, nested, _ := strings.Cut(filepath.ToSlash(path), "/")
		if rootEntry == "" || (nested != "" && kind != "directory") {
			panic("invalid embedded platform layout owned-entry path: " + path)
		}
		if existing, ok := kinds[rootEntry]; ok && existing != kind {
			panic("conflicting embedded platform layout owned-entry kinds: " + rootEntry)
		}
		kinds[rootEntry] = kind
	}
	add(layout.CurrentLink, "symlink")
	add(layout.VersionRoots.Path, "directory")
	add(layout.SharedStateDirectory, "directory")
	add(layout.InstallerStateDirectory, "directory")
	add(layout.OwnershipRecord, "file")
	for _, entry := range layout.OwnedEntries {
		rootEntry, _, _ := strings.Cut(filepath.ToSlash(entry), "/")
		if _, ok := kinds[rootEntry]; !ok {
			panic("embedded platform layout has an owned entry without a declared kind: " + entry)
		}
	}
	return kinds
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
