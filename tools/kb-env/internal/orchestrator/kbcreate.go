package orchestrator

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/env/internal/config"
	"github.com/kb-labs/env/internal/env"
)

// Install requests the profile's components through kb-create's V2 install
// contract. Progress and failures use its JSONL result stream.
func Install(kbcreateBin string, l env.Layout, profile config.Profile, registry string) error {
	logPath := filepath.Join(l.Logs, "install.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create install log: %w", err)
	}
	defer logFile.Close()

	storeDir := filepath.Join(l.Home, ".pnpm-store")
	home, err := os.UserHomeDir()
	if err != nil {
		return diag.New("KB_RUNTIME_UNEXPECTED", "The launcher release index path could not be determined.")
	}
	index := filepath.Join(home, ".local", "share", "kb-create", "release-index.json")
	args := []string{"install", "--json", "--index", index, "--platform-root", l.Platform, "--project-root", l.Project,
		"--service-profile", "default", "--plugins", strings.Join(profile.Plugins, ","), "--registry", registry}
	cmd := exec.Command(kbcreateBin, args...)
	cmd.Dir = l.Home
	// Isolated pnpm store per environment: the platform uses fixed versions
	// (e.g. 2.94.0) that may also exist in the user's global store from a prior
	// install. A dedicated store forces every @kb-labs/* package to come fresh
	// from Verdaccio, never a stale same-version copy. (Docker gives e2e this
	// isolation for free; on the host we set it explicitly.)
	cmd.Env = append(l.ExecEnv(nil), "npm_config_store_dir="+storeDir)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Run(); err != nil {
		if failure := streamFailure(logPath); failure != nil {
			return failure
		}
		return diag.New("KB_RUNTIME_UNEXPECTED", "kb-create install failed without a structured error result.",
			diag.WithMeta(map[string]any{"logPath": logPath}),
		)
	}
	return nil
}

func streamFailure(path string) *diag.Diag {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record struct {
			Error *diag.Diag `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Error != nil {
			return record.Error
		}
	}
	return nil
}
