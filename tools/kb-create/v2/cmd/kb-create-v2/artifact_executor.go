package v2cli

import (
	"io"

	"github.com/kb-labs/create/v2/artifacts"
	"github.com/kb-labs/create/v2/versionroot"
)

func newArtifactExecutor(layout versionroot.Layout, root, registry string, offline bool, log io.Writer,
	runner artifacts.Runner) (artifacts.Composite, error) {
	cacheRoot, err := layout.SharedRuntimePath(".kb/v2/cache")
	if err != nil {
		return artifacts.Composite{}, err
	}
	return artifacts.Composite{
		Packages: artifacts.Pnpm{Root: root, CacheRoot: cacheRoot, PackageManagerRoot: layout.PackageManagerStateDir(), Registry: registry, Offline: offline, Log: log, Runner: runner},
		Binaries: artifacts.Binaries{Root: root, CacheRoot: cacheRoot, Offline: offline},
	}, nil
}
