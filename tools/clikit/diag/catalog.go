package diag

// legacyCodes maps the retired Go tool error identifiers into the platform
// catalog. Callers should emit the stable code directly.
var legacyCodes = map[string]string{
	"ERR_CONFIG_LOAD":           "KB_CONFIG_LOAD_FAILED",
	"ERR_CONFIG_INVALID":        "KB_CONFIG_INVALID",
	"ERR_CONFIG_DELIVERY":       "KB_DEPLOY_CONFIG_DELIVERY",
	"ERR_HOST_UNDEFINED":        "KB_DEPLOY_HOST_UNDEFINED",
	"ERR_WRITE_LOCK":            "KB_DEPLOY_LOCK_WRITE_FAILED",
	"ERR_WAVE_FAILED":           "KB_DEPLOY_WAVE_FAILED",
	"ERR_PROFILE_MISMATCH":      "KB_ENV_PROFILE_MISMATCH",
	"ERR_CONFIG_APPLY_FAILED":   "KB_CONFIG_APPLY_FAILED",
	"ERR_OVERLAY_NOT_FOUND":     "KB_CONFIG_OVERLAY_NOT_FOUND",
	"ERR_INSTALL_FAILED":        "KB_INSTALL_FAILED",
	"ERR_HEALTH_TIMEOUT":        "KB_SERVICE_HEALTH_TIMEOUT",
	"ERR_CONFIG_NOT_FOUND":      "KB_CONFIG_NOT_FOUND",
	"ERR_REGISTRY_INTEGRITY":    "KB_MARKETPLACE_REGISTRY_INTEGRITY",
	"ERR_INSTALL_NO_RELEASE_ID": "KB_INSTALL_RELEASE_ID_MISSING",
}

// PlatformCode returns the stable catalog code for a known legacy identifier.
// Unknown identifiers become the generic unexpected code at the boundary.
func PlatformCode(code string) string {
	if mapped, ok := legacyCodes[code]; ok {
		return mapped
	}
	if len(code) > 3 && code[:3] == "KB_" {
		return code
	}
	return "KB_RUNTIME_UNEXPECTED"
}
