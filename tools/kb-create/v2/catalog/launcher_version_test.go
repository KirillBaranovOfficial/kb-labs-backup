package catalog

import (
	"errors"
	"testing"

	"github.com/kb-labs/clikit/diag"
)

func TestCheckLauncherUsesSharedSemVerRangeAndRejectsInvalidVersions(t *testing.T) {
	platform := PlatformBundle{Version: "3.0.0", MinLauncherVersion: "2.0.0-rc.2"}
	if err := CheckLauncher(platform, "2.0.0-rc.10"); err != nil {
		t.Fatalf("newer prerelease should pass: %v", err)
	}
	if err := CheckLauncher(platform, "2.0.0-rc.1"); err == nil {
		t.Fatal("older prerelease should fail")
	}
	if err := CheckLauncher(platform, "invalid"); err == nil {
		t.Fatal("invalid launcher version must fail")
	}
	platform.MinLauncherVersion = "01.0.0"
	var typed *diag.Diag
	if err := CheckLauncher(platform, "2.0.0"); !errors.As(err, &typed) || typed.Code != "KB_INSTALL_RELEASE_INDEX_INVALID" {
		t.Fatalf("invalid minimum version = %v", err)
	}
}
