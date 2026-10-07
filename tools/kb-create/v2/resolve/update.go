package resolve

import (
	"fmt"
	"github.com/kb-labs/clikit/diag"

	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/semrange"
)

// ValidateUpdate checks downgrade and installed extension compatibility before
// an update can create a snapshot, stop the host, or write package/config state.
func ValidateUpdate(active contracts.InstallReceipt, target contracts.ResolvedInstallPlan, source catalog.Catalog) error {
	installedVersion := active.Plan.ServiceGraph.PlatformVersion
	targetVersion := target.ServiceGraph.PlatformVersion
	comparison, err := semrange.Compare(targetVersion, installedVersion)
	if err != nil {
		return updateIncompatible("platform", "", targetVersion, "", "the selected platform version is invalid")
	}
	if comparison < 0 {
		failure := contracts.NewDiag(contracts.CodeUpdateDowngradeRefused,
			fmt.Sprintf("The requested platform version %s is older than installed version %s.", targetVersion, installedVersion),
			"use `kb-create rollback`", nil)
		failure.Meta = map[string]any{"installedVersion": installedVersion, "targetVersion": targetVersion}
		return failure
	}
	targetSDK := sdkVersion(target, source)
	for _, artifact := range active.Plan.Artifacts {
		// Bundled platform members can also be discoverable registry entities.
		// Their exact package versions are owned by the selected platform release;
		// only independently installed plugin/adapter artifacts need separate
		// manifest-range checks here.
		if artifact.Kind != "plugin" && artifact.Kind != "adapter" {
			continue
		}
		kind := artifact.Kind
		compatibilityRange := artifact.PlatformRange
		if compatibilityRange != "" {
			if err := compatible(compatibilityRange, targetVersion, kind+" "+artifact.ID+" platform"); err != nil {
				return updateIncompatible(kind, artifact.ID, targetVersion, compatibilityRange, err.Error())
			}
		}
		if artifact.SDKRange != "" && targetSDK != "" {
			if err := compatible(artifact.SDKRange, targetSDK, kind+" "+artifact.ID+" SDK"); err != nil {
				return updateIncompatible(kind, artifact.ID, targetSDK, artifact.SDKRange, err.Error())
			}
		}
	}
	return nil
}

func sdkVersion(target contracts.ResolvedInstallPlan, source catalog.Catalog) string {
	for _, artifact := range target.Artifacts {
		if artifact.Kind == "sdk" {
			return artifact.Version
		}
	}
	if target.Request.SDK.Version != "" {
		return target.Request.SDK.Version
	}
	if target.Request.SDK.Channel != "" {
		return source.Channels[target.Request.SDK.Channel]
	}
	return ""
}

func updateIncompatible(kind, id, version, rangeValue, reason string) *diag.Diag {
	name := kind
	if id != "" {
		name += " " + id
	}
	message := fmt.Sprintf("The update is incompatible with installed %s at %s: %s.", name, version, reason)
	hint := "Review the installed component compatibility before updating."
	if kind == "plugin" && id != "" {
		hint = fmt.Sprintf("Update or remove plugin %s first, then rerun `kb-create update`.", id)
	}
	if kind == "adapter" && id != "" {
		hint = fmt.Sprintf("Update or remove adapter %s first, then rerun `kb-create update`.", id)
	}
	return compatibilityFailure(contracts.CodeUpdateIncompatible, id, kind, id, version, rangeValue,
		message, hint, nil)
}
