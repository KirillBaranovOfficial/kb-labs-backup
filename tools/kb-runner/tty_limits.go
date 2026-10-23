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

var (
	ttyPercentPattern = regexp.MustCompile(`(?i)([0-9]{1,3}(?:\.[0-9]+)?)\s*%`)
	ttyResetPattern   = regexp.MustCompile(`(?i)reset(?:s|ting)?\s+(?:in\s+)?([0-9]+\s*(?:h|hours?|m|minutes?|s|seconds?)(?:\s+[0-9]+\s*(?:h|hours?|m|minutes?|s|seconds?))*)`)
	ttyNumberUnit     = regexp.MustCompile(`(?i)([0-9]+)\s*(h|hours?|m|minutes?|s|seconds?)`)
)

func readTTYLimits(parent context.Context, vendor, binary string) []VendorLimit {
	ctx, cancel := context.WithTimeout(parent, ttyProbeTimeout)
	defer cancel()
	text, err := probeTTY(ctx, vendor, binary)
	if err != nil {
		return unavailableLimits(vendor, "tty", err)
	}
	limits := parseTTYLimits(vendor, text, time.Now)
	if len(limits) == 0 {
		return unavailableLimits(vendor, "tty", errors.New("unrecognized vendor quota screen layout"))
	}
	return limits
}

func probeTTY(ctx context.Context, vendor, binary string) (string, error) {
	master, slave, err := openPTY()
	if err != nil {
		return "", fmt.Errorf("open pseudo-terminal: %w", err)
	}
	defer master.Close()
	defer slave.Close()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start %s interactive limits probe: %w", vendor, err)
	}
	_ = slave.Close()
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	waited := false
	chunks := make(chan []byte, 8)
	readDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, readErr := master.Read(buffer)
			if n > 0 {
				chunks <- append([]byte(nil), buffer[:n]...)
			}
			if readErr != nil {
				readDone <- readErr
				return
			}
		}
	}()
	defer func() {
		if !waited && cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		if !waited {
			select {
			case <-wait:
			case <-time.After(time.Second):
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				<-wait
			}
		}
	}()

	marker, ok := ttyPromptMarker(vendor)
	if !ok {
		return "", fmt.Errorf("no interactive prompt contract for %s", vendor)
	}
	var output bytes.Buffer
	if err := readToPrompt(ctx, chunks, readDone, &output, marker, 0); err != nil {
		return "", fmt.Errorf("wait for %s ready prompt: %w", vendor, err)
	}
	helpStart := output.Len()
	if _, err := master.Write([]byte("/help\r")); err != nil {
		return "", fmt.Errorf("request %s in-session help: %w", vendor, err)
	}
	if err := readToPrompt(ctx, chunks, readDone, &output, marker, helpStart); err != nil {
		return "", fmt.Errorf("read %s in-session help: %w", vendor, err)
	}
	help := ansi.Strip(string(output.Bytes()[helpStart:]))
	command, err := discoverLimitCommand(help)
	if err != nil {
		return "", fmt.Errorf("discover %s usage command from in-session help: %w", vendor, err)
	}
	statsStart := output.Len()
	if _, err := master.Write([]byte(command + "\r")); err != nil {
		return "", fmt.Errorf("request %s limits: %w", vendor, err)
	}
	if err := readToPrompt(ctx, chunks, readDone, &output, marker, statsStart); err != nil {
		return "", fmt.Errorf("read %s quota screen: %w", vendor, err)
	}
	if _, err := master.Write([]byte{4}); err != nil {
		return "", fmt.Errorf("exit %s limits probe: %w", vendor, err)
	}
	select {
	case <-wait:
		waited = true
	case <-ctx.Done():
		return "", fmt.Errorf("stop %s limits probe: %w", vendor, ctx.Err())
	case <-time.After(time.Second):
		return "", fmt.Errorf("%s limits command did not exit after Ctrl-D", vendor)
	}
	return ansi.Strip(string(output.Bytes()[statsStart:])), nil
}

func readToPrompt(ctx context.Context, chunks <-chan []byte, readDone <-chan error, output *bytes.Buffer, marker string, start int) error {
	for {
		if start <= output.Len() && strings.Contains(ansi.Strip(string(output.Bytes()[start:])), marker) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk := <-chunks:
			_, _ = output.Write(chunk)
		case err := <-readDone:
			if err == nil || errors.Is(err, io.EOF) {
				return io.EOF
			}
			return err
		}
	}
}

func ttyPromptMarker(vendor string) (string, bool) {
	switch vendor {
	case "gemini":
		return "> ", true
	case "claude":
		return "❯", true
	case "codex":
		return "›", true
	default:
		return "", false
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
