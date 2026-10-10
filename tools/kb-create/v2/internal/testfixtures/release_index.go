package testfixtures

import (
	"encoding/json"
	"fmt"
)

// ReleaseIndexJSON returns one complete release-index fixture shared by v2
// package tests. The platform, SDK, and shipped binary relations stay valid
// together; callers can decode it and vary unrelated test inputs afterward.
func ReleaseIndexJSON(platformVersion, sdkVersion, os, arch string) []byte {
	platformID := "platform@" + platformVersion
	sdkID := "sdk@" + sdkVersion
	index := map[string]any{
		"channels": map[string]string{"stable": platformVersion},
		"compatibility": map[string]any{
			"schema": "kb.release-compatibility/2",
			"labels": []any{
				map[string]any{"id": platformID, "kind": "platform", "artifactId": "platform", "version": platformVersion,
					"requires": []any{map[string]any{"label": sdkID}}, "status": "prepared", "validatedBy": []string{"test"}},
				map[string]any{"id": sdkID, "kind": "sdk", "artifactId": "sdk", "version": sdkVersion,
					"status": "prepared", "validatedBy": []string{"test"}},
				map[string]any{"id": fmt.Sprintf("binary:kb-create@%s:%s/%s", platformVersion, os, arch), "kind": "binary", "artifactId": "kb-create", "version": platformVersion,
					"requires": []any{map[string]any{"label": platformID}, map[string]any{"label": sdkID}}, "status": "prepared", "validatedBy": []string{"test"}},
			},
		},
		"platforms": []any{map[string]any{
			"id": "platform", "version": platformVersion, "package": "@kb/platform", "tarball": "https://example.test/platform.tgz", "sha256": "platform",
			"host":     map[string]any{"command": "kb-host", "healthUrl": "http://127.0.0.1:4000/health"},
			"profiles": map[string]any{"default": map[string]any{}},
			"binaries": []any{map[string]any{"id": "kb-create", "os": os, "arch": arch, "url": "https://example.test/kb-create", "sha256": "binary", "filename": "kb-create"}},
		}},
		"sdks": []any{map[string]any{"id": "sdk", "version": sdkVersion, "package": "@kb/sdk", "tarball": "https://example.test/sdk.tgz", "sha256": "sdk"}},
	}
	data, err := json.Marshal(index)
	if err != nil {
		panic(fmt.Errorf("marshal shared release-index fixture: %w", err))
	}
	return data
}
