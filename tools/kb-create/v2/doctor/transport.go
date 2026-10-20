package doctor

import (
	"encoding/json"
	"fmt"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/versionroot"
)

// Input is a manifest-derived, value-free doctor request. Release tooling
// produces it from selected package manifests; the launcher handles only the
// declared requirement shape and presence state, never secret material.
type Input struct {
	Manifests  []Manifest      `json:"manifests"`
	Configured map[string]bool `json:"configured"`
}

// Response is the doctor report. Installation and Platform answer different
// questions (see sections.go) and are present when a platform root was given;
// Findings and Repair are the manifest-requirement gaps of the installation.
type Response struct {
	OK               bool                      `json:"ok"`
	Installed        bool                      `json:"installed"`
	Running          bool                      `json:"running"`
	Reachable        bool                      `json:"reachable"`
	Next             string                    `json:"next"`
	PlatformChannel  string                    `json:"platformChannel,omitempty"`
	PlatformVersion  string                    `json:"platformVersion,omitempty"`
	InstalledVersion string                    `json:"installedVersion,omitempty"`
	RunningVersion   string                    `json:"runningVersion,omitempty"`
	PendingRestart   *contracts.PendingRestart `json:"pendingRestart,omitempty"`
	Retention        *RetentionReport          `json:"retention,omitempty"`
	Profile          string                    `json:"profile,omitempty"`
	Installation     *Section                  `json:"installation,omitempty"`
	Platform         *Section                  `json:"platform,omitempty"`
	Findings         []Finding                 `json:"findings"`
	Repair           RepairPlan                `json:"repair"`
}

type RetentionReport struct {
	InstallRoot       string                        `json:"installRoot"`
	PlatformRoot      string                        `json:"platformRoot"`
	VersionsDirectory string                        `json:"versionsDirectory"`
	SharedState       string                        `json:"sharedState"`
	InstallerState    string                        `json:"installerState"`
	HostState         string                        `json:"hostState"`
	Versions          []versionroot.RetainedVersion `json:"versions"`
	Snapshots         []receipt.SnapshotUsage       `json:"snapshots"`
}

func Decode(data []byte) (Input, error) {
	var input Input
	if err := json.Unmarshal(data, &input); err != nil {
		return Input{}, fmt.Errorf("decode V2 doctor input: %w", err)
	}
	if input.Configured == nil {
		input.Configured = map[string]bool{}
	}
	for _, manifest := range input.Manifests {
		if manifest.ID == "" {
			return Input{}, fmt.Errorf("doctor manifest ID is required")
		}
		for _, requirement := range manifest.Requirements {
			if requirement.Secret && requirement.ID == "" {
				return Input{}, fmt.Errorf("secret doctor requirement in %s needs an ID", manifest.ID)
			}
		}
	}
	return input, nil
}

func DiagnoseJSON(data []byte) (Response, error) {
	input, err := Decode(data)
	if err != nil {
		return Response{}, err
	}
	findings := Diagnose(input.Manifests, input.Configured)
	return Response{OK: !HasErrors(findings), Findings: findings, Repair: PlanRepair(findings)}, nil
}
