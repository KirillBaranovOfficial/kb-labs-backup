package host

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

const ProcessIdentityEnv = "KB_CREATE_PROCESS_IDENTITY"

func NewProcessIdentityToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func currentProcessIdentity() (string, error) {
	token, ok := os.LookupEnv(ProcessIdentityEnv)
	if !ok || token == "" {
		return "", errors.New("process identity token is missing from this process environment")
	}
	return token, nil
}

func ProcessEnvironmentWithIdentity(env []string, token string) []string {
	return append(ProcessEnvironmentWithoutIdentity(env), ProcessIdentityEnv+"="+token)
}

func ProcessEnvironmentWithoutIdentity(env []string) []string {
	result := make([]string, 0, len(env))
	prefix := ProcessIdentityEnv + "="
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return result
}

func processIdentityFromEnvironment(data []byte) (string, error) {
	prefix := []byte(ProcessIdentityEnv + "=")
	for _, entry := range bytes.Split(data, []byte{0}) {
		if bytes.HasPrefix(entry, prefix) {
			token := string(entry[len(prefix):])
			if token != "" {
				return token, nil
			}
		}
	}
	return "", errors.New("process identity token is absent from process environment")
}

func processIdentityMatchesWithReader(pid int, expected string, readIdentity func(int) (string, error)) bool {
	if pid <= 0 || expected == "" || !processAlive(pid) {
		return false
	}
	actual, err := readIdentity(pid)
	return err == nil && actual == expected
}

func signalWithProcessIdentity(pid int, expected string, signal func() error) bool {
	return signalWithIdentityReader(pid, expected, ProcessIdentityForPID, signal)
}

func signalWithIdentityReader(pid int, expected string, readIdentity func(int) (string, error), signal func() error) bool {
	if !processIdentityMatchesWithReader(pid, expected, readIdentity) {
		return false
	}
	return signal() == nil
}

func ProcessIdentityMatches(pid int, expected string) bool {
	return processIdentityMatchesWithReader(pid, expected, ProcessIdentityForPID)
}
