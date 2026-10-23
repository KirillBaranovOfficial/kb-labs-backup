//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const ttyProbeTimeout = 12 * time.Second
const ttyStableScreenQuiet = 700 * time.Millisecond

type ttyProbeResult struct {
	Limits []VendorLimit
	Screen string
	Steps  []LimitStepTiming
	Err    error
}

func readTTYLimitsDetailed(parent context.Context, vendor, binary string) ttyProbeResult {
	ctx, cancel := context.WithTimeout(parent, ttyProbeTimeout)
	defer cancel()
	probe := probeTTY(ctx, vendor, binary)
	if probe.Err != nil {
		return ttyProbeResult{Limits: unavailableLimits(vendor, "tty", probe.Err), Screen: probe.Screen, Steps: probe.Steps, Err: probe.Err}
	}
	limits := parseQuotaScreen(vendor, probe.Screen, time.Now)
	if len(limits) == 0 {
		err := errors.New("unrecognized vendor quota screen layout")
		if strings.TrimSpace(probe.Screen) != "" {
			err = errors.New(redactLimitText(strings.TrimSpace(probe.Screen)))
		}
		return ttyProbeResult{Limits: unavailableLimits(vendor, "tty", err), Screen: probe.Screen, Steps: probe.Steps, Err: err}
	}
	return ttyProbeResult{Limits: limits, Screen: probe.Screen, Steps: probe.Steps}
}

func probeTTY(ctx context.Context, vendor, binary string) (result ttyProbeResult) {
	master, slave, err := openPTY()
	if err != nil {
		return ttyProbeResult{Err: fmt.Errorf("open pseudo-terminal: %w", err)}
	}
	masterOpen, slaveOpen := true, true
	var cmd *exec.Cmd
	waited := false
	var wait chan error
	defer func() {
		var cleanupErr error
		if !waited && cmd != nil && cmd.Process != nil {
			if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, os.ErrProcessDone) {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("kill %s limits process group: %w", vendor, err))
			}
			select {
			case <-wait:
				waited = true
			case <-time.After(time.Second):
				if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
					cleanupErr = errors.Join(cleanupErr, fmt.Errorf("kill %s limits process: %w", vendor, err))
				}
				<-wait
				waited = true
			}
		}
		if slaveOpen {
			if err := slave.Close(); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close pseudo-terminal slave: %w", err))
			}
		}
		if masterOpen {
			if err := master.Close(); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close pseudo-terminal master: %w", err))
			}
		}
		if cleanupErr != nil {
			result.Err = errors.Join(result.Err, cleanupErr)
		}
	}()
	cmd = exec.CommandContext(ctx, binary)
	cmd.Env, err = vendorChildEnvironment(vendor)
	if err != nil {
		return ttyProbeResult{Err: fmt.Errorf("build %s terminal environment: %w", vendor, err)}
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return ttyProbeResult{Err: fmt.Errorf("start %s interactive limits probe: %w", vendor, err)}
	}
	wait = make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	if err := slave.Close(); err != nil {
		return ttyProbeResult{Err: fmt.Errorf("close pseudo-terminal slave after start: %w", err)}
	}
	slaveOpen = false
	chunks := make(chan []byte, 8)
	readDone := make(chan error, 1)
	responder := &terminalQueryResponder{}
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, readErr := master.Read(buffer)
			if n > 0 {
				visible, replies := responder.Feed(buffer[:n])
				for _, reply := range replies {
					if _, writeErr := master.Write(reply); writeErr != nil {
						readDone <- fmt.Errorf("answer terminal query: %w", writeErr)
						return
					}
				}
				if len(visible) > 0 {
					select {
					case chunks <- visible:
					case <-ctx.Done():
						return
					}
				}
			}
			if readErr != nil {
				readDone <- readErr
				return
			}
		}
	}()
	var output terminalScreen
	steps := make([]LimitStepTiming, 0, 4)
	measure := func(name string, fn func() error) error {
		started := time.Now()
		err := fn()
		steps = append(steps, LimitStepTiming{Step: name, DurationMS: time.Since(started).Milliseconds()})
		return err
	}
	if err := measure("ready", func() error { return readUntilStableScreen(ctx, chunks, readDone, &output) }); err != nil {
		return ttyProbeResult{Screen: output.String(), Steps: steps, Err: fmt.Errorf("wait for stable %s screen: %w", vendor, err)}
	}
	if _, err := master.Write([]byte("/help\r")); err != nil {
		return ttyProbeResult{Screen: output.String(), Steps: steps, Err: fmt.Errorf("request %s in-session help: %w", vendor, err)}
	}
	if err := measure("help", func() error { return readUntilStableScreen(ctx, chunks, readDone, &output) }); err != nil {
		return ttyProbeResult{Screen: output.String(), Steps: steps, Err: fmt.Errorf("read %s in-session help: %w", vendor, err)}
	}
	help := output.String()
	command, err := discoverLimitCommand(help)
	if err != nil {
		return ttyProbeResult{Screen: help, Steps: steps, Err: fmt.Errorf("discover %s usage command from in-session help: %w", vendor, err)}
	}
	if _, err := master.Write([]byte(command + "\r")); err != nil {
		return ttyProbeResult{Screen: output.String(), Steps: steps, Err: fmt.Errorf("request %s limits: %w", vendor, err)}
	}
	if err := measure("usage", func() error { return readUntilStableScreen(ctx, chunks, readDone, &output) }); err != nil {
		return ttyProbeResult{Screen: output.String(), Steps: steps, Err: fmt.Errorf("read %s quota screen: %w", vendor, err)}
	}
	if _, err := master.Write([]byte{4}); err != nil {
		return ttyProbeResult{Screen: output.String(), Steps: steps, Err: fmt.Errorf("exit %s limits probe: %w", vendor, err)}
	}
	exitStart := time.Now()
	select {
	case waitErr := <-wait:
		waited = true
		if waitErr != nil {
			return ttyProbeResult{Screen: output.String(), Steps: steps, Err: fmt.Errorf("exit %s limits command: %w", vendor, waitErr)}
		}
	case <-ctx.Done():
		steps = append(steps, LimitStepTiming{Step: "exit", DurationMS: time.Since(exitStart).Milliseconds()})
		return ttyProbeResult{Screen: output.String(), Steps: steps, Err: fmt.Errorf("stop %s limits probe: %w", vendor, ctx.Err())}
	case <-time.After(time.Second):
		steps = append(steps, LimitStepTiming{Step: "exit", DurationMS: time.Since(exitStart).Milliseconds()})
		return ttyProbeResult{Screen: output.String(), Steps: steps, Err: fmt.Errorf("%s limits command did not exit after Ctrl-D", vendor)}
	}
	steps = append(steps, LimitStepTiming{Step: "exit", DurationMS: time.Since(exitStart).Milliseconds()})
	return ttyProbeResult{Screen: output.String(), Steps: steps}
}

func readUntilStableScreen(ctx context.Context, chunks <-chan []byte, readDone <-chan error, output *terminalScreen) error {
	var stable *time.Timer
	var stableC <-chan time.Time
	sawOutput := false
	defer func() {
		if stable != nil {
			stable.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk := <-chunks:
			if len(chunk) == 0 {
				continue
			}
			sawOutput = true
			_, _ = output.Write(chunk)
			if stable == nil {
				stable = time.NewTimer(ttyStableScreenQuiet)
			} else {
				if !stable.Stop() {
					select {
					case <-stable.C:
					default:
					}
				}
				stable.Reset(ttyStableScreenQuiet)
			}
			stableC = stable.C
		case <-stableC:
			if sawOutput {
				return nil
			}
		case err := <-readDone:
			if err == nil || errors.Is(err, io.EOF) {
				return io.EOF
			}
			return err
		}
	}
}

func discoverLimitCommand(help string) (string, error) {
	for _, line := range strings.Split(help, "\n") {
		fields := strings.Fields(line)
		for index, field := range fields {
			if !strings.HasPrefix(field, "/") {
				continue
			}
			lower := strings.ToLower(line)
			if !strings.Contains(lower, "usage") && !strings.Contains(lower, "quota") && !strings.Contains(lower, "limit") && !strings.Contains(lower, "stats") {
				continue
			}
			command := strings.TrimRight(field, ",:.)]")
			if strings.Contains(command, "stats") && strings.Contains(lower, "model") {
				return command + " model", nil
			}
			if index+1 < len(fields) && fields[index+1] == "[usage]" {
				command += " usage"
			}
			return command, nil
		}
	}
	return "", errors.New("help listed no usage, quota, limit, or stats command")
}

func openPTY() (*os.File, *os.File, error) {
	masterFD, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	closeMaster := func(err error) (*os.File, *os.File, error) {
		_ = unix.Close(masterFD)
		return nil, nil, err
	}
	var slaveName string
	if runtime.GOOS == "darwin" {
		if err := unix.IoctlSetInt(masterFD, unix.TIOCPTYGRANT, 0); err != nil {
			return closeMaster(err)
		}
		if err := unix.IoctlSetInt(masterFD, unix.TIOCPTYUNLK, 0); err != nil {
			return closeMaster(err)
		}
		var name [128]byte
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(masterFD), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0])))
		if errno != 0 {
			return closeMaster(errno)
		}
		slaveName = string(bytes.TrimRight(name[:], "\x00"))
	} else {
		if err := unix.IoctlSetInt(masterFD, 0x40045431, 0); err != nil {
			return closeMaster(err)
		}
		var number uint32
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(masterFD), uintptr(0x80045430), uintptr(unsafe.Pointer(&number)))
		if errno != 0 {
			return closeMaster(errno)
		}
		slaveName = fmt.Sprintf("/dev/pts/%d", number)
	}
	slaveFD, err := unix.Open(slaveName, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return closeMaster(err)
	}
	if err := unix.IoctlSetWinsize(masterFD, unix.TIOCSWINSZ, &unix.Winsize{Row: ttyScreenRows, Col: ttyScreenCols}); err != nil {
		_ = unix.Close(slaveFD)
		return closeMaster(err)
	}
	return os.NewFile(uintptr(masterFD), "pty-master"), os.NewFile(uintptr(slaveFD), "pty-slave"), nil
}
