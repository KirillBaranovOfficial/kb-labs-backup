package supervisor

import (
	"context"
	"errors"
	"github.com/kb-labs/clikit/diag"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/control"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/versionroot"
)

type stubRunner struct{ calls []string }

func (r *stubRunner) Name() string { return "stub" }
func (r *stubRunner) Start(context.Context) (host.Info, error) {
	r.calls = append(r.calls, "start")
	return host.Info{Runner: "stub"}, nil
}
func (r *stubRunner) Stop(context.Context) (host.StopResult, error) {
	r.calls = append(r.calls, "stop")
	return host.StopResult{Runner: "stub"}, nil
}
func (r *stubRunner) Status(context.Context) (host.Status, error) {
	return host.Status{Runner: "stub", State: host.StateRunning, Healthy: true}, nil
}

func installedRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	layout, err := versionroot.New(root)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := layout.StageInitial()
	if err != nil {
		t.Fatal(err)
	}
	installationID, err := versionroot.InstallationIDForPath(stage)
	if err != nil {
		t.Fatal(err)
	}
	plan := contracts.ResolvedInstallPlan{Schema: contracts.ResolvedPlanSchema, Host: &contracts.HostSpec{Command: "host", HealthURL: "http://127.0.0.1:4000/health"},
		ServiceGraph: contracts.ServiceGraph{PlatformVersion: "1.0.0", Services: []contracts.Service{{ID: "gateway"}}}}
	if err := receipt.Write(stage, contracts.InstallReceipt{ID: "test-receipt", InstallationID: installationID, Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if _, err := layout.ActivateInitial(stage, "1.0.0", "test-plan"); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRunPublishesControlChannelAndStopsHostOnCancel(t *testing.T) {
	token, err := host.NewProcessIdentityToken()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(host.ProcessIdentityEnv, token)
	state := host.State{Dir: t.TempDir()}
	root := installedRoot(t)
	runner := &stubRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (&Supervisor{Runner: runner, State: state, PlatformRoot: root, Version: "t"}).Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	var client *control.Client
	for {
		var err error
		if client, err = control.Connect(state); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("control channel never appeared: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status, err := client.Status(context.Background()); err != nil || status.Host.State != host.StateRunning {
		t.Fatalf("status: %+v %v", status, err)
	}
	installationID, err := versionroot.InstallationIDForPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if record, ok := readRecordFile(state); !ok || record.InstallationID != installationID {
		t.Fatalf("supervisor record identity = %#v, %t; want %q", record, ok, installationID)
	}
	// A second supervisor for the same state home must refuse to start.
	err = (&Supervisor{Runner: &stubRunner{}, State: state, PlatformRoot: root}).Run(context.Background())
	var typed *diag.Diag
	if !errors.As(err, &typed) || typed.Code != "KB_HOST_ALREADY_RUNNING" {
		t.Fatalf("second supervisor: %v", err)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := runner.calls; len(got) != 2 || got[0] != "start" || got[1] != "stop" {
		t.Fatalf("runner calls: %v", got)
	}
	for _, path := range []string{state.Control(), state.SupervisorPID()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed on shutdown", path)
		}
	}
}

func TestLivePIDRemovesStalePidFile(t *testing.T) {
	state := host.State{Dir: t.TempDir()}
	if err := os.WriteFile(state.SupervisorPID(), []byte(strconv.Itoa(2147483646)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, alive := LivePID(state, t.TempDir()); alive {
		t.Fatal("dead pid reported alive")
	}
	if _, err := os.Stat(state.SupervisorPID()); !os.IsNotExist(err) {
		t.Fatal("stale supervisor.pid must be removed")
	}
}

func TestSupervisorRejectsAReusedPIDWithoutStartingOrSignallingIt(t *testing.T) {
	token, err := host.NewProcessIdentityToken()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(host.ProcessIdentityEnv, token)
	state := host.State{Dir: t.TempDir()}
	if err := state.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.SupervisorPID(), []byte(`{"pid":`+strconv.Itoa(os.Getpid())+`,"processIdentity":"`+token+`","platformRoot":"/tmp/kb"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &stubRunner{}
	err = (&Supervisor{Runner: runner, State: state, PlatformRoot: "/tmp/kb"}).Run(context.Background())
	failure, ok := err.(*diag.Diag)
	if !ok || failure.Code != contracts.CodeHostProcessIdentityStale {
		t.Fatalf("stale supervisor diagnostic = %#v", err)
	}
	if len(runner.calls) != 0 || !host.ProcessAlive(os.Getpid()) {
		t.Fatalf("stale supervisor record affected its reused PID or started a host: calls=%v", runner.calls)
	}
}
