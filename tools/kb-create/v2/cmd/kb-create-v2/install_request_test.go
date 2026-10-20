package v2cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
)

// case: S-040.1
func TestInstallWizardPreservesCallerRequestFields(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	base := contracts.InstallRequest{
		PlatformRoot: "/tmp/platform", ProjectRoot: canonicalProjectRoot,
		Platform: contracts.VersionSelector{Channel: contracts.ChannelStable}, ServiceProfile: "default",
	}
	request, err := mergeDirectRequest(base, directRequest{
		GatewayURL: "http://127.0.0.1:4700", PlatformVersion: "2.4.1", SDKVersion: "3.2.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.GatewayURL != "http://127.0.0.1:4700" || request.Platform.Version != "2.4.1" || request.SDK.Version != "3.2.0" {
		t.Fatalf("caller fields were lost during wizard request compilation: %#v", request)
	}
	if request.PlatformRoot != base.PlatformRoot || request.ProjectRoot != canonicalProjectRoot || request.ServiceProfile != base.ServiceProfile {
		t.Fatalf("scenario request fields were lost during merge: %#v", request)
	}
}
