//go:build windows

package preflight

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

type memoryStatusEx struct {
	length, memoryLoad                                                             uint32
	totalPhys, availPhys, totalPageFile, availPageFile, totalVirtual, availVirtual uint64
	availExtendedVirtual                                                           uint64
}

func availableMemory() (uint64, error) {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")
	status := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	result, _, err := proc.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return 0, err
	}
	return status.availPhys, nil
}

func availableDisk(root string) (uint64, error) {
	path, err := existingParent(root)
	if err != nil {
		return 0, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(name, &available, &total, &free); err != nil {
		return 0, err
	}
	return available, nil
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
