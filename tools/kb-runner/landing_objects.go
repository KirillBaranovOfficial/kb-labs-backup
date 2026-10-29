//go:build darwin || linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type landingObject struct {
	CommitSHA gitObjectID
	TreeSHA   gitObjectID
}

type landingRefConflict struct {
	Ref      string
	Expected string
	Current  string
}

type integrationRefConflict struct {
	Ref      string
	Expected string
}

func (conflict *integrationRefConflict) Error() string {
	return fmt.Sprintf("integration branch %s compare-and-swap refused landing at expected commit %s; no candidate was submitted", conflict.Ref, conflict.Expected)
}

func (conflict *landingRefConflict) Error() string {
	if conflict.Current == "" {
		return fmt.Sprintf("landing ref %s changed before compare-and-swap from expected commit %s; no resolver commit was submitted", conflict.Ref, conflict.Expected)
	}
	return fmt.Sprintf("landing ref %s changed before compare-and-swap: expected %s, found %s", conflict.Ref, conflict.Expected, conflict.Current)
}

func buildLandingObjects(stateIO runnerStateStore, repoRoot, stream, verifiedTree, expectedOld string, commits []proposedCommit, jobTemp string) (result landingObject, resultErr error) {
	verifiedTreeID, err := storedGitObjectID(verifiedTree)
	if err != nil {
		return landingObject{}, errors.New("landing requires full verified tree and expected ref commit IDs")
	}
	expectedOldID, err := storedGitObjectID(expectedOld)
	if err != nil {
		return landingObject{}, errors.New("landing requires full verified tree and expected ref commit IDs")
	}
	if _, _, err := (verificationPush{kind: verificationPushWorker, stream: stream, sha: expectedOldID.String()}).ref(); err != nil {
		return landingObject{}, fmt.Errorf("validate landing stream: %w", err)
	}
	parentTree, err := treeSHAForCommit(repoRoot, expectedOldID.String())
	if err != nil {
		return landingObject{}, err
	}
	groups, err := landingPathGroups(repoRoot, parentTree.String(), verifiedTreeID.String())
	if err != nil {
		return landingObject{}, err
	}
	if err := validateCommitGroups(commits, groups[:]); err != nil {
		return landingObject{}, err
	}
	if err := os.MkdirAll(jobTemp, 0o700); err != nil {
		return landingObject{}, fmt.Errorf("create landing job temp: %w", err)
	}
	indexDir, err := os.MkdirTemp(jobTemp, "land-index-")
	if err != nil {
		return landingObject{}, fmt.Errorf("create private landing index directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(indexDir); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove private landing index directory %s: %w", indexDir, err))
		}
	}()
	indexPath := filepath.Join(indexDir, "index")
	if _, err := gitWithIndex(stateIO, repoRoot, indexPath, "read-tree", parentTree.String()); err != nil {
		return landingObject{}, fmt.Errorf("initialize private landing index: %w", err)
	}
	entries, err := landingTreeEntries(repoRoot, verifiedTreeID.String())
	if err != nil {
		return landingObject{}, err
	}
	parentCommit := expectedOldID
	result = landingObject{CommitSHA: expectedOldID, TreeSHA: parentTree}
	for groupIndex, groupPaths := range groups {
		if len(groupPaths) == 0 {
			continue
		}
		for _, path := range groupPaths {
			entry, exists := entries[path]
			if !exists {
				if _, err := gitWithIndex(stateIO, repoRoot, indexPath, "update-index", "--force-remove", "--", path); err != nil {
					return landingObject{}, fmt.Errorf("remove deleted verified path %q from landing index: %w", path, err)
				}
				continue
			}
			cacheInfo := entry.mode + "," + entry.oid.String() + "," + path
			if _, err := gitWithIndex(stateIO, repoRoot, indexPath, "update-index", "--add", "--cacheinfo", cacheInfo); err != nil {
				return landingObject{}, fmt.Errorf("overlay verified path %q into landing index: %w", path, err)
			}
		}
		tree, err := gitWithIndexObjectID(stateIO, repoRoot, indexPath, "write-tree")
		if err != nil {
			return landingObject{}, fmt.Errorf("write %s landing tree: %w", commits[groupIndex].Kind, err)
		}
		messages := []string{commits[groupIndex].Subject}
		if groupIndex == 2 && strings.TrimSpace(commits[groupIndex].Body) != "" {
			messages = append(messages, commits[groupIndex].Body)
		}
		commitSHA, err := createCommitObject(stateIO, repoRoot, tree, []gitObjectID{parentCommit}, messages...)
		if err != nil {
			return landingObject{}, fmt.Errorf("create %s landing commit object: %w", commits[groupIndex].Kind, err)
		}
		parentCommit = commitSHA
		result = landingObject{CommitSHA: commitSHA, TreeSHA: tree}
	}
	if result.TreeSHA != verifiedTreeID {
		return landingObject{}, fmt.Errorf("landing object tree %s does not reproduce verified tree %s", result.TreeSHA, verifiedTree)
	}
	return result, nil
}

func createCommitObject(stateIO runnerStateStore, repoRoot string, tree gitObjectID, parents []gitObjectID, messages ...string) (gitObjectID, error) {
	if _, err := readGitObjectID(tree.String()); err != nil || len(parents) == 0 || len(messages) == 0 {
		return "", errors.New("commit object requires a full tree, at least one full parent, and a message")
	}
	args := []string{"-C", repoRoot, "commit-tree", tree.String()}
	for _, parent := range parents {
		if _, err := readGitObjectID(parent.String()); err != nil {
			return "", fmt.Errorf("commit object parent %q is not a full commit SHA", parent)
		}
		args = append(args, "-p", parent.String())
	}
	for _, message := range messages {
		if strings.TrimSpace(message) == "" {
			return "", errors.New("commit object message cannot be empty")
		}
		args = append(args, "-m", message)
	}
	sha, err := readGitObjectIDMutation(stateIO, repoRoot, nil, args[2:]...)
	if err != nil {
		return "", fmt.Errorf("create commit object: %w", err)
	}
	return sha, nil
}

func readVerifiedWorkerReport(task SchedulerTask, tree string) ([]byte, error) {
	path := ".agent-report.md"
	if task.Vendor == "zcode" {
		path = filepath.ToSlash(filepath.Join("reports", task.Stream+".md"))
	}
	command := exec.Command("git", "-C", task.RepoRoot, "show", tree+":"+path)
	data, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read %s from verified tree %s: %w", path, tree, err)
	}
	return data, nil
}

type landingTreeEntry struct {
	mode string
	oid  gitObjectID
}

func landingTreeEntries(repoRoot, tree string) (map[string]landingTreeEntry, error) {
	output, err := schedulerCommandOutput(repoRoot, "git", "-C", repoRoot, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		return nil, fmt.Errorf("read verified tree entries: %w", err)
	}
	entries := make(map[string]landingTreeEntry)
	for _, record := range strings.Split(output, "\x00") {
		if record == "" {
			continue
		}
		metadata, path, ok := strings.Cut(record, "\t")
		if !ok {
			return nil, fmt.Errorf("decode verified tree entry %q", record)
		}
		fields := strings.Fields(metadata)
		if len(fields) != 3 {
			return nil, fmt.Errorf("decode verified tree entry metadata %q", metadata)
		}
		objectID, idErr := readGitObjectID(fields[2])
		if idErr != nil {
			return nil, fmt.Errorf("decode verified tree entry metadata %q: %w", metadata, idErr)
		}
		entries[path] = landingTreeEntry{mode: fields[0], oid: objectID}
	}
	return entries, nil
}

func landingPathGroups(repoRoot, parentTree, verifiedTree string) ([3][]string, error) {
	all, err := landingDiffPaths(repoRoot, parentTree, verifiedTree, nil)
	if err != nil {
		return [3][]string{}, err
	}
	casePaths, err := landingDiffPaths(repoRoot, parentTree, verifiedTree, []string{":(glob)docs/qa/**"})
	if err != nil {
		return [3][]string{}, err
	}
	testPaths, err := landingDiffPaths(repoRoot, parentTree, verifiedTree, []string{":(glob)**/__tests__/**", ":(glob)**/*.test.*", ":(glob)**/*.spec.*", ":(glob)**/*_test.go", ":(glob)e2e/journey/test_*.py", ":(glob)**/__snapshots__/**", ":(glob)**/fixtures/**", ":(glob)**/testdata/**"})
	if err != nil {
		return [3][]string{}, err
	}
	caseSet, testSet := make(map[string]bool, len(casePaths)), make(map[string]bool, len(testPaths))
	for _, path := range casePaths {
		caseSet[path] = true
	}
	for _, path := range testPaths {
		testSet[path] = true
	}
	groups := [3][]string{}
	for _, path := range all {
		switch {
		case caseSet[path]:
			groups[0] = append(groups[0], path)
		case testSet[path]:
			groups[1] = append(groups[1], path)
		default:
			groups[2] = append(groups[2], path)
		}
	}
	for index := range groups {
		sort.Strings(groups[index])
	}
	return groups, nil
}

func landingDiffPaths(repoRoot, parentTree, verifiedTree string, pathspecs []string) ([]string, error) {
	args := []string{"-C", repoRoot, "diff", "--no-renames", "--name-only", "-z", parentTree, verifiedTree}
	if len(pathspecs) > 0 {
		args = append(args, "--")
		args = append(args, pathspecs...)
	}
	output, err := schedulerCommandOutput(repoRoot, "git", args...)
	if err != nil {
		return nil, fmt.Errorf("list immutable landing paths: %w", err)
	}
	var paths []string
	for _, path := range strings.Split(output, "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func moveLandingRef(stateIO runnerStateStore, repoRoot, stream string, object landingObject, expectedOld string) error {
	expectedOldID, err := storedGitObjectID(expectedOld)
	if err != nil {
		return fmt.Errorf("landing expected ref has invalid object ID: %w", err)
	}
	ref := "refs/heads/agent/" + stream
	if err := compareAndSwapLandingRef(stateIO, repoRoot, ref, object.CommitSHA, expectedOldID); err == nil {
		return nil
	} else {
		var conflict *landingRefConflict
		if !errors.As(err, &conflict) {
			return err
		}
		current, readErr := readGitObjectIDCommand(repoRoot, "-C", repoRoot, "rev-parse", "--verify", ref)
		if readErr != nil {
			return errors.Join(err, fmt.Errorf("read landing ref after refusal: %w", readErr))
		}
		if current != expectedOldID {
			conflict.Current = current.String()
			return conflict
		}
		return fmt.Errorf("compare-and-swap landing ref %s: %w", ref, err)
	}
}

func compareAndSwapLandingRef(stateIO runnerStateStore, repoRoot, ref string, newCommit, expectedOld gitObjectID) error {
	if _, err := readGitObjectID(newCommit.String()); err != nil {
		return errors.New("landing ref compare-and-swap requires full commit SHAs")
	}
	if _, err := readGitObjectID(expectedOld.String()); err != nil {
		return errors.New("landing ref compare-and-swap requires full commit SHAs")
	}
	if _, err := stateIO.GitMutation(repoRoot, nil, "update-ref", ref, newCommit.String(), expectedOld.String()); err != nil {
		return &landingRefConflict{Ref: ref, Expected: expectedOld.String()}
	}
	return nil
}

func refreshLandingWorktree(stateIO runnerStateStore, repoRoot, workdir, commitSHA string) error {
	id, err := storedGitObjectID(commitSHA)
	if err != nil {
		return fmt.Errorf("refresh landing worktree with invalid commit ID: %w", err)
	}
	if _, err := stateIO.GitMutation(workdir, nil, "reset", "--mixed", id.String()); err != nil {
		return fmt.Errorf("refresh worker HEAD and index to landed commit without rewriting worktree files: %w", err)
	}
	return nil
}
