//go:build !windows

package preflight

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func availableMemory() (uint64, error) {
	if runtime.GOOS == "linux" {
		file, err := os.Open("/proc/meminfo")
		if err != nil {
			return 0, err
		}
		defer file.Close()
		var available uint64
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 && fields[0] == "MemAvailable:" {
				value, parseErr := strconv.ParseUint(fields[1], 10, 64)
				if parseErr != nil {
					return 0, parseErr
				}
				available = value * 1024
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return 0, err
		}
		if available == 0 {
			return 0, fmt.Errorf("MemAvailable is missing from /proc/meminfo")
		}
		return linuxCgroupAvailable(available)
	}
	if runtime.GOOS == "darwin" {
		output, err := exec.Command("vm_stat").Output()
		if err != nil {
			return 0, err
		}
		return darwinAvailableMemory(string(output))
	}
	return 0, fmt.Errorf("memory preflight is not implemented for %s", runtime.GOOS)
}

func darwinAvailableMemory(output string) (uint64, error) {
	var pages uint64
	for _, line := range strings.Split(output, "\n") {
		fields := strings.SplitN(line, ":", 2)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if name != "Pages free" && name != "Pages inactive" && name != "Pages speculative" && name != "Pages purgeable" {
			continue
		}
		value := strings.Trim(strings.TrimSpace(fields[1]), ".")
		value = strings.ReplaceAll(value, ",", "")
		count, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return 0, err
		}
		pages += count
	}
	if pages == 0 {
		return 0, fmt.Errorf("vm_stat did not report available pages")
	}
	return pages * uint64(unix.Getpagesize()), nil
}

func linuxCgroupAvailable(hostAvailable uint64) (uint64, error) {
	for _, pair := range [][2]string{
		{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory.current"},
		{"/sys/fs/cgroup/memory/memory.limit_in_bytes", "/sys/fs/cgroup/memory/memory.usage_in_bytes"},
	} {
		limitData, limitErr := os.ReadFile(pair[0])
		currentData, currentErr := os.ReadFile(pair[1])
		if limitErr != nil || currentErr != nil {
			continue
		}
		limitText, currentText := strings.TrimSpace(string(limitData)), strings.TrimSpace(string(currentData))
		if limitText == "max" {
			return hostAvailable, nil
		}
		limit, err := strconv.ParseUint(limitText, 10, 64)
		if err != nil {
			return 0, err
		}
		current, err := strconv.ParseUint(currentText, 10, 64)
		if err != nil {
			return 0, err
		}
		if limit > 0 && limit < 1<<60 {
			remaining := uint64(0)
			if current < limit {
				remaining = limit - current
			}
			if remaining < hostAvailable {
				hostAvailable = remaining
			}
		}
		break
	}
	return hostAvailable, nil
}

func availableDisk(root string) (uint64, error) {
	path, err := existingParent(root)
	if err != nil {
		return 0, err
	}
	var stats unix.Statfs_t
	if err := unix.Statfs(path, &stats); err != nil {
		return 0, err
	}
	return uint64(stats.Bavail) * uint64(stats.Bsize), nil
}

func existingParent(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("platform root is empty")
	}
	path = filepath.Clean(path)
	for {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", fmt.Errorf("no existing parent for platform root")
		}
		path = parent
	}
}
