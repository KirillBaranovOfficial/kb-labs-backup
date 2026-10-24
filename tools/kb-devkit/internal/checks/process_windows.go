//go:build windows

package checks

import "os/exec"

func setProcessGroup(*exec.Cmd) {}
