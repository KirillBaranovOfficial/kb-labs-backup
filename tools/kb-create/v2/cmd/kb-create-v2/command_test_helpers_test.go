package v2cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/versionroot"
	"github.com/spf13/cobra"
)

func writeVersionedReceipt(t *testing.T, root string, installed contracts.InstallReceipt) {
	t.Helper()
	layout, err := versionroot.New(root)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := layout.StageInitial()
	if err != nil {
		t.Fatal(err)
	}
	installed.InstallationID, err = versionroot.InstallationIDForPath(stage)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Write(stage, installed); err != nil {
		t.Fatal(err)
	}
	version := installed.Plan.ServiceGraph.PlatformVersion
	if version == "" {
		version = "1.0.0"
	}
	if _, err := layout.ActivateInitial(stage, version, installed.Plan.PlanHash); err != nil {
		t.Fatal(err)
	}
}

func assertEmptyOrMissing(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil || len(entries) != 0 {
		t.Fatalf("directory %s contains entries after a read-only operation: %v, %v", path, entries, err)
	}
}

func platformRootForTest(t *testing.T, installRoot string) string {
	t.Helper()
	layout, err := versionroot.New(installRoot)
	if err != nil {
		t.Fatal(err)
	}
	return layout.PlatformRoot()
}

// operationTestSetup is a test-only fixture for supplying typed command inputs
// and dependency seams. Production code receives command-local typed inputs.
type operationTestSetup struct {
	Operation, Index, Input, DoctorInput, PlatformRoot, Snapshot, Registry, SecretEnv string
	Fix                                                                               bool
	Recover                                                                           recoverRequest
	ScenarioID, ScenarioAnswers                                                       string
	ScenarioResume                                                                    bool
	Direct                                                                            directRequest
	Executable                                                                        string
	Foreground, JSON, Yes                                                             bool
	InstallInput                                                                      io.Reader
	InstallOutput                                                                     io.Writer
	RuntimeDeps                                                                       *runtime.Dependencies
	NodePreflight                                                                     func() error
	ProjectRegistrar                                                                  func(string) (projectRegistrationResult, error)
}

func executeForTest(t *testing.T, setup operationTestSetup) (any, *diag.Diag, int, string) {
	t.Helper()
	descriptor, ok := lookupOperationDescriptor(setup.Operation)
	if !ok {
		t.Fatalf("command %q is not registered", setup.Operation)
	}
	var stdout bytes.Buffer
	cmd := &cobra.Command{Use: "kb-create " + setup.Operation}
	cmd.SetOut(&stdout)
	cmd.Flags().Bool("json", setup.JSON, "emit machine-readable output")
	values := make(map[string]any, len(descriptor.Flags))
	registerLocalFlags(cmd, descriptor.Flags, values)
	reader := newCommandFlagReader(cmd, values)
	setTestFlagValues(t, setup, reader)
	deps := commandDependencies{Runtime: setup.RuntimeDeps, NodeCheck: setup.NodePreflight,
		InstallInput: setup.InstallInput, InstallOutput: setup.InstallOutput, Command: cmd, Executable: setup.Executable}
	if deps.Runtime == nil {
		deps.Runtime = &runtime.Dependencies{}
	}
	if deps.NodeCheck == nil {
		deps.NodeCheck = func() error { return nil }
	}
	deps.ProjectRegistrar = func(root, project string) (projectRegistrationResult, error) {
		return registerInstalledProject(root, project, setup.ProjectRegistrar)
	}
	data, failure := descriptor.Handler(context.Background(), cmd, reader, deps)
	if failure != nil {
		return nil, failure, diag.ExitCode(failure), stdout.String()
	}
	if completion, ok := data.(streamCompletion); ok {
		return nil, nil, completion.ExitCode, stdout.String()
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return data, nil, 0, string(encoded)
}

func setTestFlagValues(t *testing.T, setup operationTestSetup, reader *commandFlagReader) {
	if t != nil {
		t.Helper()
	}
	setupValue := reflect.ValueOf(setup)
	directValue := reflect.ValueOf(setup.Direct)
	recoverValue := reflect.ValueOf(setup.Recover)
	for _, name := range operationFlags(setup.Operation) {
		value := reader.values[name]
		var source reflect.Value
		switch name {
		case "platform-root":
			source = setupValue.FieldByName("PlatformRoot")
			if directValue.FieldByName("PlatformRoot").String() != "" {
				source = directValue.FieldByName("PlatformRoot")
			}
		case "project-root":
			source = directValue.FieldByName("ProjectRoot")
		case "platform-version":
			source = directValue.FieldByName("PlatformVersion")
		case "platform-channel":
			source = directValue.FieldByName("PlatformChannel")
		case "gateway-url":
			source = directValue.FieldByName("GatewayURL")
		case "sdk-version":
			source = directValue.FieldByName("SDKVersion")
		case "service-profile":
			source = directValue.FieldByName("ServiceProfile")
		case "plugins":
			source = directValue.FieldByName("Plugins")
		case "adapters":
			source = directValue.FieldByName("Adapters")
		case "provider":
			source = directValue.FieldByName("Providers")
		case "policy":
			source = directValue.FieldByName("Policy")
		case "offline":
			source = directValue.FieldByName("Offline")
		case "scenario":
			source = setupValue.FieldByName("ScenarioID")
		case "fix":
			source = setupValue.FieldByName("Fix")
		case "recover":
			source = recoverValue.FieldByName("Enabled")
		case "yes":
			if setup.Operation == "doctor" {
				source = recoverValue.FieldByName("Yes")
			} else {
				source = setupValue.FieldByName("Yes")
			}
		case "resume":
			source = setupValue.FieldByName("ScenarioResume")
		case "email":
			source = recoverValue.FieldByName("Email")
		case "tenant":
			source = recoverValue.FieldByName("Tenant")
		case "password-stdin":
			source = recoverValue.FieldByName("PasswordStdin")
		case "generate":
			source = recoverValue.FieldByName("Generate")
		default:
			source = setupValue.FieldByName(flagFieldName(name))
		}
		if !source.IsValid() {
			continue
		}
		switch target := value.(type) {
		case *string:
			if source.Kind() == reflect.String {
				*target = source.String()
			}
		case *bool:
			if source.Kind() == reflect.Bool {
				*target = source.Bool()
			}
		case *repeatedValue:
			if source.Kind() == reflect.String && source.String() != "" {
				*target = strings.Split(source.String(), ",")
			}
		}
	}
}

func flagFieldName(flag string) string {
	parts := strings.Split(flag, "-")
	for i := range parts {
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}

func operationFlags(name string) []string {
	for _, descriptor := range operationDescriptors {
		if descriptor.Name == name {
			return descriptor.Flags
		}
	}
	return nil
}

func descriptorTestCall(cmd *cobra.Command, descriptor operationDescriptor, setup operationTestSetup) error {
	cmd.Flags().Bool("json", setup.JSON, "emit machine-readable output")
	values := make(map[string]any, len(descriptor.Flags))
	registerLocalFlags(cmd, descriptor.Flags, values)
	reader := newCommandFlagReader(cmd, values)
	setTestFlagValues(nil, setup, reader)
	deps := commandDependencies{Runtime: setup.RuntimeDeps, NodeCheck: setup.NodePreflight,
		InstallInput: setup.InstallInput, InstallOutput: setup.InstallOutput, Command: cmd, Executable: setup.Executable}
	if deps.Runtime == nil {
		deps.Runtime = &runtime.Dependencies{}
	}
	if deps.NodeCheck == nil {
		deps.NodeCheck = func() error { return nil }
	}
	deps.ProjectRegistrar = func(root, project string) (projectRegistrationResult, error) {
		return registerInstalledProject(root, project, setup.ProjectRegistrar)
	}
	return callDescriptor(cmd, descriptor, values, deps)
}

func invoke(t *testing.T, input operationTestSetup) (map[string]any, int, string) {
	t.Helper()
	data, failure, code, output := executeForTest(t, input)
	if failure != nil {
		encoded, err := json.Marshal(result.FailureEnvelope(failure))
		if err != nil {
			t.Fatal(err)
		}
		output = string(encoded)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("command output is not JSON: %v: %s", err, output)
	}
	if parsed == nil {
		parsed = map[string]any{}
	}
	if _, exists := parsed["ok"]; !exists && failure == nil && data != nil {
		parsed["ok"] = true
	}
	return parsed, code, output
}
