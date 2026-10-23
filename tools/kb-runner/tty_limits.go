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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/unix"
)

const ttyProbeTimeout = 12 * time.Second
const ttyStableScreenQuiet = 700 * time.Millisecond

var (
	ttyPercentPattern = regexp.MustCompile(`(?i)([0-9]{1,3}(?:\.[0-9]+)?)\s*%`)
	ttyResetPattern   = regexp.MustCompile(`(?i)reset(?:s|ting)?\s+(?:in\s+)?([0-9]+\s*(?:h|hours?|m|minutes?|s|seconds?)(?:\s+[0-9]+\s*(?:h|hours?|m|minutes?|s|seconds?))*)`)
	ttyNumberUnit     = regexp.MustCompile(`(?i)([0-9]+)\s*(h|hours?|m|minutes?|s|seconds?)`)
)

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
	limits := parseTTYLimits(vendor, probe.Screen, time.Now)
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
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, readErr := master.Read(buffer)
			if n > 0 {
				select {
				case chunks <- append([]byte(nil), buffer[:n]...):
				case <-ctx.Done():
					return
				}
			}
			if readErr != nil {
				readDone <- readErr
				return
			}
		}
	}()
	var output bytes.Buffer
	steps := make([]LimitStepTiming, 0, 4)
	measure := func(name string, fn func() error) error {
		started := time.Now()
		err := fn()
		steps = append(steps, LimitStepTiming{Step: name, DurationMS: time.Since(started).Milliseconds()})
		return err
	}
	if err := measure("ready", func() error { return readUntilStableScreen(ctx, chunks, readDone, &output) }); err != nil {
		return ttyProbeResult{Screen: renderedTTYScreen(output.String()), Steps: steps, Err: fmt.Errorf("wait for stable %s screen: %w", vendor, err)}
	}
	helpStart := output.Len()
	if _, err := master.Write([]byte("/help\r")); err != nil {
		return ttyProbeResult{Screen: renderedTTYScreen(output.String()), Steps: steps, Err: fmt.Errorf("request %s in-session help: %w", vendor, err)}
	}
	if err := measure("help", func() error { return readUntilStableScreen(ctx, chunks, readDone, &output) }); err != nil {
		return ttyProbeResult{Screen: renderedTTYScreen(output.String()[helpStart:]), Steps: steps, Err: fmt.Errorf("read %s in-session help: %w", vendor, err)}
	}
	help := ansi.Strip(output.String()[helpStart:])
	command, err := discoverLimitCommand(help)
	if err != nil {
		return ttyProbeResult{Screen: renderedTTYScreen(help), Steps: steps, Err: fmt.Errorf("discover %s usage command from in-session help: %w", vendor, err)}
	}
	statsStart := output.Len()
	if _, err := master.Write([]byte(command + "\r")); err != nil {
		return ttyProbeResult{Screen: renderedTTYScreen(output.String()[statsStart:]), Steps: steps, Err: fmt.Errorf("request %s limits: %w", vendor, err)}
	}
	if err := measure("usage", func() error { return readUntilStableScreen(ctx, chunks, readDone, &output) }); err != nil {
		return ttyProbeResult{Screen: renderedTTYScreen(output.String()[statsStart:]), Steps: steps, Err: fmt.Errorf("read %s quota screen: %w", vendor, err)}
	}
	if _, err := master.Write([]byte{4}); err != nil {
		return ttyProbeResult{Screen: renderedTTYScreen(output.String()[statsStart:]), Steps: steps, Err: fmt.Errorf("exit %s limits probe: %w", vendor, err)}
	}
	exitStart := time.Now()
	select {
	case waitErr := <-wait:
		waited = true
		if waitErr != nil {
			return ttyProbeResult{Screen: renderedTTYScreen(output.String()[statsStart:]), Steps: steps, Err: fmt.Errorf("exit %s limits command: %w", vendor, waitErr)}
		}
	case <-ctx.Done():
		steps = append(steps, LimitStepTiming{Step: "exit", DurationMS: time.Since(exitStart).Milliseconds()})
		return ttyProbeResult{Screen: renderedTTYScreen(output.String()[statsStart:]), Steps: steps, Err: fmt.Errorf("stop %s limits probe: %w", vendor, ctx.Err())}
	case <-time.After(time.Second):
		steps = append(steps, LimitStepTiming{Step: "exit", DurationMS: time.Since(exitStart).Milliseconds()})
		return ttyProbeResult{Screen: renderedTTYScreen(output.String()[statsStart:]), Steps: steps, Err: fmt.Errorf("%s limits command did not exit after Ctrl-D", vendor)}
	}
	steps = append(steps, LimitStepTiming{Step: "exit", DurationMS: time.Since(exitStart).Milliseconds()})
	return ttyProbeResult{Screen: renderedTTYScreen(output.String()[statsStart:]), Steps: steps}
}

func readUntilStableScreen(ctx context.Context, chunks <-chan []byte, readDone <-chan error, output *bytes.Buffer) error {
	var stable *time.Timer
	var stableC <-chan time.Time
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
			if strings.TrimSpace(renderedTTYScreen(output.String())) != "" {
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

func renderedTTYScreen(raw string) string {
	text := ansi.Strip(raw)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return redactLimitText(text)
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

func parseTTYLimits(vendor, screen string, now func() time.Time) []VendorLimit {
	clean := strings.Join(strings.Fields(ansi.Strip(screen)), " ")
	percentMatches := ttyPercentPattern.FindAllStringSubmatchIndex(clean, -1)
	if len(percentMatches) == 0 {
		return nil
	}
	reset := parseTTYReset(clean, now())
	limits := make([]VendorLimit, 0, len(percentMatches))
	for _, match := range percentMatches {
		percent, err := strconv.ParseFloat(clean[match[2]:match[3]], 64)
		if err != nil {
			return nil
		}
		prefix := strings.ToLower(clean[max(0, match[0]-64):match[0]])
		window := ""
		switch {
		case strings.Contains(prefix, "week"), strings.Contains(prefix, "7d"):
			window = "week"
		case strings.Contains(prefix, "5h"), strings.Contains(prefix, "five-hour"), strings.Contains(prefix, "5-hour"):
			window = "5h"
		case strings.Contains(prefix, "day"), strings.Contains(prefix, "daily"):
			window = "daily"
		}
		if window == "" {
			return nil
		}
		limits = append(limits, VendorLimit{Vendor: vendor, Window: window, UsedPercent: percent, ResetsAt: reset, Confidence: "vendor", Source: "tty", FetchedAt: now().UTC()})
	}
	return limits
}

func parseTTYReset(screen string, now time.Time) *time.Time {
	for _, format := range []string{time.RFC3339, "Jan 2, 2006 3:04 PM MST", "Jan 2 2006 15:04 MST"} {
		for _, token := range strings.Fields(screen) {
			if reset, err := time.Parse(format, strings.Trim(token, "(),;")); err == nil {
				return &reset
			}
		}
	}
	match := ttyResetPattern.FindStringSubmatch(screen)
	if len(match) != 2 {
		return nil
	}
	var duration time.Duration
	for _, unit := range ttyNumberUnit.FindAllStringSubmatch(match[1], -1) {
		amount, err := strconv.Atoi(unit[1])
		if err != nil {
			return nil
		}
		switch strings.ToLower(unit[2])[0] {
		case 'h':
			duration += time.Duration(amount) * time.Hour
		case 'm':
			duration += time.Duration(amount) * time.Minute
		case 's':
			duration += time.Duration(amount) * time.Second
		}
	}
	if duration <= 0 {
		return nil
	}
	reset := now.Add(duration).UTC()
	return &reset
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
	_ = unix.IoctlSetWinsize(masterFD, unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 120})
	return os.NewFile(uintptr(masterFD), "pty-master"), os.NewFile(uintptr(slaveFD), "pty-slave"), nil
}
