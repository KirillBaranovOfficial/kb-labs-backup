//go:build windows

package host

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ProcessIdentityForPID(pid int) (string, error) {
	if pid == os.Getpid() {
		return currentProcessIdentity()
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var basic windows.PROCESS_BASIC_INFORMATION
	if err := windows.NtQueryInformationProcess(handle, windows.ProcessBasicInformation, unsafe.Pointer(&basic), uint32(unsafe.Sizeof(basic)), nil); err != nil {
		return "", err
	}
	var peb windows.PEB
	if err := readProcessMemory(handle, uintptr(unsafe.Pointer(basic.PebBaseAddress)), unsafe.Pointer(&peb), unsafe.Sizeof(peb)); err != nil {
		return "", err
	}
	var parameters windows.RTL_USER_PROCESS_PARAMETERS
	if err := readProcessMemory(handle, uintptr(unsafe.Pointer(peb.ProcessParameters)), unsafe.Pointer(&parameters), unsafe.Sizeof(parameters)); err != nil {
		return "", err
	}
	size := parameters.EnvironmentSize
	if size < 2 || size%2 != 0 || size > 1024*1024 {
		return "", fmt.Errorf("process %d environment size is invalid", pid)
	}
	environment := make([]uint16, size/2)
	if err := readProcessMemory(handle, uintptr(parameters.Environment), unsafe.Pointer(&environment[0]), size); err != nil {
		return "", err
	}
	prefix := ProcessIdentityEnv + "="
	for _, entry := range strings.Split(string(utf16.Decode(environment)), "\x00") {
		if strings.HasPrefix(entry, prefix) && len(entry) > len(prefix) {
			return entry[len(prefix):], nil
		}
	}
	return "", nil
}

func readProcessMemory(handle windows.Handle, address uintptr, destination unsafe.Pointer, size uintptr) error {
	var read uintptr
	if err := windows.ReadProcessMemory(handle, address, (*byte)(destination), size, &read); err != nil {
		return err
	}
	if read != size {
		return fmt.Errorf("read %d bytes of process memory, got %d", size, read)
	}
	return nil
}
