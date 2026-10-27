//go:build darwin || linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func snapshotWorktree(worktree, stream string) (sha, ref string, resultErr error) {
	status, err := schedulerCommandOutput(worktree, "git", "-C", worktree, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", "", fmt.Errorf("inspect worktree before snapshot: %w", err)
	}
	unmerged, err := schedulerCommandOutput(worktree, "git", "-C", worktree, "ls-files", "-u")
	if err != nil {
		return "", "", fmt.Errorf("inspect unresolved index entries before snapshot: %w", err)
	}
	if unmerged != "" {
		return "", "", fmt.Errorf("cannot snapshot a worktree with unresolved merge entries; inspect the paths with: git -C %s diff --name-only --diff-filter=U", shellQuote(worktree))
	}
	head, err := schedulerCommandOutput(worktree, "git", "-C", worktree, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("resolve worktree HEAD for snapshot: %w", err)
	}
	if status == "" {
		return head, "", nil
	}
	index, err := os.CreateTemp("", "kb-runner-snapshot-index-*")
	if err != nil {
		return "", "", fmt.Errorf("create private snapshot index: %w", err)
	}
	indexPath := index.Name()
	if err := index.Close(); err != nil {
		removeErr := os.Remove(indexPath)
		return "", "", errors.Join(fmt.Errorf("close private snapshot index: %w", err), removeErr)
	}
	defer func() {
		if err := os.Remove(indexPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove private snapshot index %s: %w", indexPath, err))
		}
	}()
	if _, err := gitWithIndex(worktree, indexPath, "read-tree", "HEAD"); err != nil {
		return "", "", fmt.Errorf("initialize private snapshot index: %w", err)
	}
	if _, err := gitWithIndex(worktree, indexPath, "add", "-A"); err != nil {
		return "", "", fmt.Errorf("stage worktree into private snapshot index: %w", err)
	}
	tree, err := gitWithIndex(worktree, indexPath, "write-tree")
	if err != nil {
		return "", "", fmt.Errorf("write private snapshot tree: %w", err)
	}
	sha, err = schedulerCommandOutput(worktree, "git", "-C", worktree, "commit-tree", tree, "-p", head, "-m", "snapshot "+stream+" before agent round")
	if err != nil {
		return "", "", fmt.Errorf("create snapshot commit object: %w", err)
	}
	ref = filepath.ToSlash(filepath.Join("refs", "snapshots", stream, time.Now().UTC().Format("20060102T150405.000000000Z")))
	if _, err := schedulerCommandOutput(worktree, "git", "-C", worktree, "update-ref", ref, sha, ""); err != nil {
		return "", "", fmt.Errorf("record snapshot ref %s: %w", ref, err)
	}
	return sha, ref, nil
}

func treeSHAForCommit(worktree, sha string) (string, error) {
	if !fullGitSHA.MatchString(sha) {
		return "", fmt.Errorf("commit SHA %q is invalid", sha)
	}
	tree, err := schedulerCommandOutput(worktree, "git", "-C", worktree, "rev-parse", sha+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("resolve tree for commit %s: %w", sha, err)
	}
	if !fullGitSHA.MatchString(tree) {
		return "", fmt.Errorf("commit %s returned invalid tree SHA %q", sha, tree)
	}
	return tree, nil
}

func gitWithIndex(worktree, indexPath string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", worktree}, args...)...)
	command.Env = replaceEnvironment(os.Environ(), "GIT_INDEX_FILE", indexPath)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(output)), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func replaceEnvironment(environment []string, key, value string) []string {
	result := make([]string, 0, len(environment)+1)
	prefix := key + "="
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}
