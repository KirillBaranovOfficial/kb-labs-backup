package resolve

import (
	"fmt"
	"math"
	"sort"

	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/semrange"
)

func SummarizeUpdate(active contracts.InstallReceipt, target contracts.ResolvedInstallPlan, source catalog.Catalog) (contracts.UpdateSummary, error) {
	if err := contracts.ValidatePlanArtifactIdentities(active.Plan); err != nil {
		return contracts.UpdateSummary{}, err
	}
	if err := contracts.ValidatePlanArtifactIdentities(target); err != nil {
		return contracts.UpdateSummary{}, err
	}
	installedVersion := active.Plan.ServiceGraph.PlatformVersion
	targetVersion := target.ServiceGraph.PlatformVersion
	installed := indexUpdateArtifacts(active.Plan.Artifacts)
	planned := indexUpdateArtifacts(target.Artifacts)
	keys := make([]string, 0, len(installed)+len(planned))
	seen := make(map[string]struct{}, len(installed)+len(planned))
	for key := range installed {
		keys = append(keys, key)
		seen[key] = struct{}{}
	}
	for key := range planned {
		if _, exists := seen[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	summary := contracts.UpdateSummary{
		Components:      make([]contracts.UpdateComponent, 0, len(keys)),
		AddedPackages:   []string{},
		RemovedPackages: []string{},
		ReleaseNotes:    []contracts.UpdateReleaseNotes{},
	}
	for _, key := range keys {
		before, hadBefore := installed[key]
		after, hasAfter := planned[key]
		component := contracts.UpdateComponent{}
		if hasAfter {
			component.ID, component.Package, component.ToVersion = after.ID, after.Package, after.Version
		}
		if hadBefore {
			component.ID, component.Package, component.FromVersion = before.ID, before.Package, before.Version
		}
		summary.Components = append(summary.Components, component)
		if hasAfter && !hadBefore && after.Package != "" {
			summary.AddedPackages = append(summary.AddedPackages, after.Package)
		}
		if hadBefore && !hasAfter && before.Package != "" {
			summary.RemovedPackages = append(summary.RemovedPackages, before.Package)
		}
		if hasAfter && (!hadBefore || before.Version != after.Version) {
			if after.SizeBytes < 0 || summary.DownloadBytes > math.MaxInt64-after.SizeBytes {
				return contracts.UpdateSummary{}, fmt.Errorf("update download-size total exceeds the supported byte range")
			}
			summary.DownloadBytes += after.SizeBytes
		}
	}
	sort.Strings(summary.AddedPackages)
	sort.Strings(summary.RemovedPackages)
	platformID := "platform"
	for _, artifact := range target.Artifacts {
		if artifact.Kind == "platform" {
			platformID = artifact.ID
			break
		}
	}
	for _, release := range source.Platforms {
		if release.ID != platformID {
			continue
		}
		from, err := semrange.Compare(release.Version, installedVersion)
		if err != nil {
			return contracts.UpdateSummary{}, fmt.Errorf("compare release %s with installed platform %s: %w", release.Version, installedVersion, err)
		}
		to, err := semrange.Compare(release.Version, targetVersion)
		if err != nil {
			return contracts.UpdateSummary{}, fmt.Errorf("compare release %s with target platform %s: %w", release.Version, targetVersion, err)
		}
		if from > 0 && to <= 0 {
			note := contracts.UpdateReleaseNotes{Version: release.Version, Notes: release.ReleaseNotes}
			position := len(summary.ReleaseNotes)
			for index, existing := range summary.ReleaseNotes {
				comparison, err := semrange.Compare(note.Version, existing.Version)
				if err != nil {
					return contracts.UpdateSummary{}, fmt.Errorf("compare release-note versions %s and %s: %w", note.Version, existing.Version, err)
				}
				if comparison < 0 {
					position = index
					break
				}
			}
			summary.ReleaseNotes = append(summary.ReleaseNotes, contracts.UpdateReleaseNotes{})
			copy(summary.ReleaseNotes[position+1:], summary.ReleaseNotes[position:])
			summary.ReleaseNotes[position] = note
		}
	}
	return summary, nil
}

func indexUpdateArtifacts(artifacts []contracts.Artifact) map[string]contracts.Artifact {
	indexed := make(map[string]contracts.Artifact, len(artifacts))
	for _, artifact := range artifacts {
		key := artifact.Identity()
		indexed[key] = artifact
	}
	return indexed
}
