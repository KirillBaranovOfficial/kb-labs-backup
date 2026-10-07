package v2cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/kb-labs/clikit/stream"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/shim"
	"github.com/spf13/cobra"
)

type commandFlags struct {
	index, input, requestRoot, projectRoot, platformVersion, platformChannel  string
	sdkVersion, serviceProfile, plugins, adapters, policy, secretEnv          string
	doctorInput, scenarioID, scenarioAnswers, platformRoot, snapshotID        string
	registry, telemetryURL, operation                                         string
	providers                                                                 repeatedValue
	offline, scenarioResume, doctorFix, doctorRecover                         bool
	recoverPasswordStdin, recoverGenerate, recoverYes, foreground, jsonOutput bool
	telemetryAllowed                                                          bool
	recoverEmail, recoverTenant                                               string
}

func newRootCommand() *cobra.Command {
	flags := &commandFlags{}
	root := &cobra.Command{
		Use:           "kb-create",
		Short:         "Install, update, and diagnose a local KB Labs platform",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !cmd.Flags().Changed("operation") {
				return cmd.Help()
			}
			return runSafely(func() error { return flags.run(cmd, flags.defaultOperation(), args) })
		},
	}
	p := root.PersistentFlags()
	p.StringVar(&flags.index, "index", "", "path to immutable release index JSON")
	p.StringVar(&flags.input, "input", "", "path to install request JSON")
	p.StringVar(&flags.requestRoot, "request-platform-root", "", "platform root for a direct request")
	p.StringVar(&flags.projectRoot, "project-root", "", "project directory to register after installation")
	p.StringVar(&flags.platformVersion, "platform-version", "", "exact platform version")
	p.StringVar(&flags.platformChannel, "platform-channel", "", "platform channel: stable, canary, experimental")
	p.StringVar(&flags.sdkVersion, "sdk-version", "", "exact SDK version")
	p.StringVar(&flags.serviceProfile, "service-profile", "", "platform-owned service profile")
	p.StringVar(&flags.plugins, "plugins", "", "comma-separated plugin IDs or id@version pins")
	p.StringVar(&flags.adapters, "adapters", "", "comma-separated adapter IDs or id@version pins")
	p.Var(&flags.providers, "provider", "provider preference capability=adapter (repeatable)")
	p.StringVar(&flags.policy, "policy", "strict", "compatibility policy: strict or compatible")
	p.BoolVar(&flags.offline, "offline", false, "use the offline artifact source")
	p.StringVar(&flags.secretEnv, "secret-env", "", "requirement=ENV_VAR secret sources (never serialized)")
	p.StringVar(&flags.doctorInput, "doctor-input", "", "path to manifest-derived doctor JSON")
	p.StringVar(&flags.scenarioID, "scenario", "", "built-in install scenario ID")
	p.StringVar(&flags.scenarioAnswers, "scenario-answers", "", "JSON object of scenario answers")
	p.BoolVar(&flags.scenarioResume, "resume", false, "resume persisted non-secret scenario answers")
	p.BoolVar(&flags.doctorFix, "fix", false, "apply manifest-declared safe defaults")
	p.BoolVar(&flags.doctorRecover, "recover", false, "recover the administrator account")
	p.StringVar(&flags.recoverEmail, "email", "", "administrator email for recovery")
	p.StringVar(&flags.recoverTenant, "tenant", "", "tenant ID for recovery")
	p.BoolVar(&flags.recoverPasswordStdin, "password-stdin", false, "read recovery password from stdin")
	p.BoolVar(&flags.recoverGenerate, "generate", false, "generate and print a recovery password once")
	p.BoolVar(&flags.recoverYes, "yes", false, "confirm recovery, uninstall, or rollback")
	p.StringVar(&flags.operation, "operation", "plan", "operation for the flag-based interface")
	p.StringVar(&flags.platformRoot, "platform-root", "", "platform root for lifecycle operations")
	p.StringVar(&flags.snapshotID, "snapshot", "", "snapshot ID for rollback")
	p.StringVar(&flags.registry, "registry", "", "npm registry for exact artifact installation")
	p.StringVar(&flags.telemetryURL, "telemetry-endpoint", "", "opt-in anonymous telemetry endpoint")
	p.BoolVar(&flags.foreground, "foreground", false, "run the host directly in this process")
	p.BoolVar(&flags.jsonOutput, "json", false, "emit machine-readable output")
	p.BoolVar(&flags.telemetryAllowed, "telemetry-consent", false, "allow anonymous operational telemetry")
	for _, item := range []struct{ name, short string }{
		{"install", "Install KB Labs interactively"}, {"plan", "Resolve an install request"},
		{"apply", "Apply an install request"}, {"status", "Show installation status"},
		{"doctor", "Diagnose or repair an installation"}, {"start", "Start the local host"},
		{"stop", "Stop the local host"}, {"restart", "Restart the local host"},
		{"update", "Update the installation"}, {"uninstall", "Remove the installation"},
		{"rollback", "Restore a saved snapshot"}, {"supervise", "Run the host supervisor"},
	} {
		operation := item.name
		child := &cobra.Command{Use: operation, Short: item.short, Args: cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSafely(func() error { return flags.run(cmd, operation, args) })
			}}
		if operation == "supervise" {
			child.Hidden = true
		}
		root.AddCommand(child)
	}
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Show launcher build identity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "kb-create %s (commit %s)\n", buildVersion, buildCommit)
			return result.Return(map[string]string{"version": buildVersion, "commit": buildCommit})
		},
	})
	return root
}

func runSafely(run func() error) error {
	err := run()
	if err == nil || result.IsStreamComplete(err) {
		return err
	}
	if _, ok := result.ReturnedData(err); ok {
		return err
	}
	var typed *diag.Diag
	if errors.As(err, &typed) && typed != nil {
		if _, known := diag.LookupCatalog(typed.Code); known {
			return typed
		}
	}
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return diag.FromErrorWithLogger(err, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}
	logDir := filepath.Join(home, ".kb", "logs")
	if mkdirErr := os.MkdirAll(logDir, 0o700); mkdirErr != nil {
		return diag.FromErrorWithLogger(err, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}
	file, openErr := os.OpenFile(filepath.Join(logDir, "kb-create.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if openErr != nil {
		return diag.FromErrorWithLogger(err, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}
	defer file.Close()
	return diag.FromErrorWithLogger(err, slog.New(slog.NewJSONHandler(file, nil)))
}

func (f *commandFlags) defaultOperation() string {
	if f.operation == "" {
		return "plan"
	}
	return f.operation
}

func (f *commandFlags) run(cmd *cobra.Command, operation string, _ []string) error {
	telemetryEndpoint, telemetryConsent = f.telemetryURL, f.telemetryAllowed
	executable, _ := os.Executable()
	inv := invocation{
		Operation: operation, Index: f.index, Input: f.input, DoctorInput: f.doctorInput,
		PlatformRoot: f.platformRoot, Snapshot: f.snapshotID, Registry: f.registry,
		SecretEnv: f.secretEnv, DoctorFix: f.doctorFix,
		Recover: recoverRequest{Enabled: f.doctorRecover, Email: f.recoverEmail, Tenant: f.recoverTenant,
			PasswordStdin: f.recoverPasswordStdin, Generate: f.recoverGenerate, Yes: f.recoverYes, JSON: f.jsonOutput},
		ScenarioID: f.scenarioID, ScenarioAnswers: f.scenarioAnswers, ScenarioResume: f.scenarioResume,
		Direct: directRequest{PlatformRoot: f.requestRoot, ProjectRoot: f.projectRoot, PlatformVersion: f.platformVersion,
			PlatformChannel: f.platformChannel, SDKVersion: f.sdkVersion, ServiceProfile: f.serviceProfile,
			Plugins: f.plugins, Adapters: f.adapters, Providers: strings.Join(f.providers, ","), Policy: f.policy, Offline: f.offline},
		Executable: executable, Foreground: f.foreground, JSON: f.jsonOutput, Yes: f.recoverYes,
		InstallInput: os.Stdin,
	}
	return runInvocation(cmd, inv, operation)
}

func runInvocation(cmd *cobra.Command, inv invocation, operation string) error {
	if operation == "supervise" || isHostOperation(operation) {
		return runHostThroughBoundary(cmd, inv, operation)
	}
	humanMode := !inv.JSON
	if humanMode && operation != "install" && operation != "uninstall" && operation != "rollback" {
		inv.JSON = true
	}
	var progressStream *stream.Writer
	if inv.JSON && operation == "install" {
		progressStream = stream.New(command.StreamWriter(cmd))
		deps := inv.RuntimeDeps
		if deps == nil {
			deps = &runtime.Dependencies{}
		}
		deps.Progress = streamProgress{writer: progressStream}
		inv.RuntimeDeps = deps
	}
	output, err := os.CreateTemp("", "kb-create-result-*.json")
	if err != nil {
		return diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "The command could not start.")
	}
	path := output.Name()
	defer os.Remove(path)
	var human bytes.Buffer
	if operation == "install" && !inv.JSON {
		inv.InstallOutput = &human
	}
	exitCode := inv.run(output)
	if err := output.Close(); err != nil {
		return diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "The command output could not be read.")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "The command result could not be read.")
	}
	if exitCode != 0 {
		failure := errorFromOperationResult(data, exitCode)
		if progressStream != nil {
			var typed *diag.Diag
			if !errors.As(failure, &typed) {
				typed = diag.FromError(failure)
			}
			if err := progressStream.Fail(typed); err != nil {
				return diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "Install progress could not be written.")
			}
			streamExit := diag.ExitCode(typed)
			if exitCode == diag.ExitInput {
				streamExit = diag.ExitInput
			}
			_ = progressStream.Finish(streamExit)
			return result.StreamComplete(progressStream.ExitCode())
		}
		return failure
	}
	var payload map[string]any
	if len(bytes.TrimSpace(data)) > 0 {
		payload, err = operationResultPayload(operation, inv.DoctorFix, inv.Recover.Enabled, data)
		if err != nil {
			code, message := "KB_RUNTIME_UNEXPECTED", "The command returned an invalid result."
			if operation == "doctor" && !inv.DoctorFix && !inv.Recover.Enabled {
				message = "The doctor report could not be read."
			}
			return diag.Wrap(err, code, message)
		}
	}
	if progressStream != nil {
		if err := progressStream.Finish(diag.ExitOK); err != nil {
			return diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "Install progress could not be completed.")
		}
		return result.StreamComplete(progressStream.ExitCode())
	}
	if humanMode {
		if operation == "install" && human.Len() > 0 {
			command.Printf("%s", human.String())
		} else if operation == "status" {
			command.Printf("%s\n", hostHumanSummary(operation, payload))
		} else if operation == "doctor" && inv.Recover.Enabled {
			if output, ok := payload["output"].(string); ok && output != "" {
				command.Printf("%s\n", output)
			}
			command.Printf("Administrator account recovery completed.\n")
		} else {
			command.Printf("%s\n", operationHumanSummary(operation, payload))
		}
	}
	return result.Return(payload)
}

func operationResultPayload(operation string, doctorFix, doctorRecover bool, data []byte) (map[string]any, error) {
	if operation == "doctor" && !doctorFix && !doctorRecover {
		return doctorResultPayload(data)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	delete(payload, "ok")
	return payload, nil
}

func doctorResultPayload(data []byte) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	healthy, ok := payload["ok"].(bool)
	if !ok {
		return nil, errors.New("doctor report has no boolean health result")
	}
	delete(payload, "ok")
	payload["operation"] = "doctor"
	payload["status"] = "unhealthy"
	if healthy {
		payload["status"] = "healthy"
	}
	return payload, nil
}

type streamProgress struct{ writer *stream.Writer }

func (p streamProgress) Emit(event runtime.ProgressEvent) {
	state := "completed"
	if event.Start {
		state = "started"
	}
	_ = p.writer.Event(map[string]any{"phase": event.Phase, "state": state})
}

func errorFromOperationResult(data []byte, exitCode int) error {
	var envelope struct {
		Error          json.RawMessage `json:"error"`
		LogPath        string          `json:"logPath"`
		DiagnosticPath string          `json:"diagnosticPath"`
	}
	if json.Unmarshal(data, &envelope) == nil && len(envelope.Error) > 0 {
		var result struct {
			Code          string         `json:"code"`
			Message       string         `json:"message"`
			Hint          string         `json:"hint"`
			Cause         string         `json:"cause"`
			CorrelationID string         `json:"correlationId"`
			Details       map[string]any `json:"details"`
			Meta          map[string]any `json:"meta"`
		}
		if json.Unmarshal(envelope.Error, &result) == nil && result.Code != "" {
			if _, known := diag.LookupCatalog(result.Code); known {
				var typed diag.Diag
				if json.Unmarshal(envelope.Error, &typed) == nil {
					if envelope.LogPath != "" || envelope.DiagnosticPath != "" {
						if typed.Meta == nil {
							typed.Meta = map[string]any{}
						}
						if envelope.LogPath != "" {
							typed.Meta["logPath"] = envelope.LogPath
						}
						if envelope.DiagnosticPath != "" {
							typed.Meta["diagnosticPath"] = envelope.DiagnosticPath
						}
					}
					typed.ProcessExitCode = exitCode
					return &typed
				}
				meta := result.Details
				if meta == nil {
					meta = result.Meta
				}
				meta = cloneOperationMeta(meta)
				if envelope.LogPath != "" {
					meta["logPath"] = envelope.LogPath
				}
				if envelope.DiagnosticPath != "" {
					meta["diagnosticPath"] = envelope.DiagnosticPath
				}
				return diag.New(result.Code, result.Message,
					diag.WithHint(result.Hint), diag.WithCause(result.Cause),
					diag.WithCorrelationID(result.CorrelationID), diag.WithMeta(meta), diag.WithProcessExitCode(exitCode))
			}
			if exitCode == diag.ExitInput {
				meta := result.Details
				if meta == nil {
					meta = result.Meta
				}
				return diag.New("KB_RUNTIME_INPUT_INVALID", result.Message,
					diag.WithHint(result.Hint), diag.WithCause(result.Cause),
					diag.WithCorrelationID(result.CorrelationID), diag.WithMeta(meta))
			}
		}
	}
	if exitCode == diag.ExitInput {
		return diag.New("KB_RUNTIME_INPUT_INVALID", "The command input is invalid.")
	}
	return errors.New("kb-create command failed unexpectedly")
}

func cloneOperationMeta(meta map[string]any) map[string]any {
	cloned := make(map[string]any, len(meta))
	for key, value := range meta {
		cloned[key] = value
	}
	return cloned
}

func runHostThroughBoundary(cmd *cobra.Command, inv invocation, operation string) error {
	output, err := os.CreateTemp("", "kb-create-host-*.json")
	if err != nil {
		return diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "The host command could not start.")
	}
	path := output.Name()
	defer os.Remove(path)
	hostCommand := inv.hostCommand(output)
	hostCommand.JSON = true
	hostCommand.Out, hostCommand.Err = output, io.Discard
	exitCode := hostCommand.run(operation)
	if err := output.Close(); err != nil {
		return diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "The host command result could not be read.")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "The host command result could not be read.")
	}
	if exitCode != 0 {
		return errorFromOperationResult(data, exitCode)
	}
	var payload map[string]any
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &payload); err != nil {
			return diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "The host command returned an invalid result.")
		}
		delete(payload, "ok")
	}
	if inv.JSON {
		return result.Return(payload)
	}
	command.Printf("%s\n", hostHumanSummary(operation, payload))
	return result.Return(payload)
}

func operationHumanSummary(operation string, payload map[string]any) string {
	switch operation {
	case "doctor":
		lines := []string{"Platform diagnosis completed."}
		if status, _ := payload["status"].(string); status != "" {
			lines = append(lines, "Status: "+status)
		}
		if channel, _ := payload["platformChannel"].(string); channel != "" {
			lines = append(lines, "Platform channel: "+channel)
		}
		if version, _ := payload["platformVersion"].(string); version != "" {
			lines = append(lines, "Platform version: "+version)
		}
		if findings, ok := payload["findings"].([]any); ok {
			for _, raw := range findings {
				finding, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				line := "Finding " + stringField(finding, "code")
				if component := stringField(finding, "component"); component != "" {
					line += ": package=" + component
				}
				if path := stringField(finding, "path"); path != "" {
					line += " path=" + path
				}
				lines = append(lines, line)
				if hint := stringField(finding, "hint"); hint != "" {
					lines = append(lines, "Hint: "+hint)
				}
			}
		}
		for _, sectionName := range []string{"installation", "platform"} {
			section, _ := payload[sectionName].(map[string]any)
			checks, _ := section["checks"].([]any)
			for _, raw := range checks {
				check, ok := raw.(map[string]any)
				if !ok || check["ok"] == true {
					continue
				}
				line := strings.ToUpper(sectionName[:1]) + sectionName[1:] + " check " + stringField(check, "id")
				if code := stringField(check, "code"); code != "" {
					line += " (" + code + ")"
				}
				if detail := stringField(check, "detail"); detail != "" {
					line += ": " + detail
				}
				lines = append(lines, line)
				if hint := stringField(check, "hint"); hint != "" {
					lines = append(lines, "Next: "+hint)
				}
			}
		}
		return strings.Join(lines, "\n")
	case "update":
		return "Platform update completed."
	case "rollback":
		return "Platform rollback completed."
	case "apply":
		if status, ok := payload["shim"].(map[string]any); ok && status["linked"] == false {
			owner, _ := status["owner"].(string)
			return fmt.Sprintf("Platform installation completed.\nThe kb command is owned by %s; run %s to use this platform.", owner, shim.Target(installRequestRoot(payload)))
		}
		return "Platform installation completed."
	case "plan":
		return "Install plan resolved."
	case "uninstall":
		return "Platform uninstalled."
	default:
		return fmt.Sprintf("%s completed.", operation)
	}
}

func installRequestRoot(payload map[string]any) string {
	installed, _ := payload["receipt"].(map[string]any)
	plan, _ := installed["plan"].(map[string]any)
	request, _ := plan["request"].(map[string]any)
	root, _ := request["platformRoot"].(string)
	return root
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func hostHumanSummary(operation string, payload map[string]any) string {
	switch operation {
	case "status":
		installed, _ := payload["installed"].(bool)
		running, _ := payload["running"].(bool)
		reachable, _ := payload["reachable"].(bool)
		installation, _ := payload["installation"].(map[string]any)
		channel, _ := installation["platformChannel"].(string)
		version, _ := installation["platformVersion"].(string)
		return fmt.Sprintf("Installed: %t\nPlatform channel: %s\nPlatform version: %s\nRunning: %t\nReachable: %t", installed, channel, version, running, reachable)
	case "start":
		return "KB Labs host started."
	case "stop":
		return "KB Labs host stopped."
	case "restart":
		return "KB Labs host restarted."
	default:
		return "KB Labs host supervisor started."
	}
}
