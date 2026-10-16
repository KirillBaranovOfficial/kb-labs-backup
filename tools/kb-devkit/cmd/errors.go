package cmd

import (
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/devkit/internal/engine"
)

func commandFailure(command string) error {
	return diag.New("KB_RUNTIME_COMMAND_FAILED", "The command could not complete.",
		diag.WithMeta(map[string]any{"command": command}))
}

func taskFailure(result engine.TaskResult, data any) error {
	code := "KB_DEVKIT_TASK_FAILED"
	meta := map[string]any{
		"package": result.Package,
		"task":    result.Task,
		"logPath": result.LogPath,
	}
	if result.LogError != "" {
		code = "KB_DEVKIT_TASK_LOG_UNAVAILABLE"
		meta["logDirectory"] = result.LogDirectory
		meta["logError"] = result.LogError
	}
	return diag.New(code, "",
		diag.WithMeta(meta),
		diag.WithData(data))
}

func taskGraphFailure(err *engine.TaskGraphError) error {
	return diag.New("KB_DEVKIT_TASK_GRAPH_INVALID", "",
		diag.WithMeta(map[string]any{
			"package":        err.Package,
			"task":           err.Task,
			"dependency":     err.Dependency,
			"dependencyTask": err.DependencyTask,
		}),
	)
}
