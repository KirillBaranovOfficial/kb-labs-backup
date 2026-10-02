package cmd

import "github.com/kb-labs/clikit/diag"

// Stable platform error codes used by kb-deploy.
const (
	codeWaveFailed     = "KB_DEPLOY_WAVE_FAILED"
	codeConfigLoad     = "KB_CONFIG_LOAD_FAILED"
	codeConfigInvalid  = "KB_CONFIG_INVALID"
	codeConfigDelivery = "KB_DEPLOY_CONFIG_DELIVERY"
	codeHostUndefined  = "KB_DEPLOY_HOST_UNDEFINED"
	codeWriteLock      = "KB_DEPLOY_LOCK_WRITE_FAILED"
)

func init() {
	diag.RegisterHints(map[string]string{
		codeWaveFailed:     "Inspect the reported service failures, fix their cause, then re-run `kb-deploy apply`.",
		codeConfigLoad:     "check the deploy manifest path (--config) and that it is readable",
		codeConfigInvalid:  "the deploy manifest or rendered kb.config.jsonc is invalid — validate its syntax and required fields",
		codeConfigDelivery: "config delivery to meta.host failed before any release changed — check SSH connectivity and disk space, then re-run; partially delivered hosts were restored",
		codeHostUndefined:  "the plan references a host with no SSH connection — check hosts[] in deploy.yaml",
		codeWriteLock:      "the apply succeeded but the lock could not be written — check filesystem permissions next to the manifest",
	})
}
