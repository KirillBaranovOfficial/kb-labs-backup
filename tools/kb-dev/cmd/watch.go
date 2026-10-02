package cmd

import (
	"fmt"
	"sync"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	clikitstream "github.com/kb-labs/clikit/stream"
	"github.com/kb-labs/dev/internal/manager"
	"github.com/spf13/cobra"
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Stream service events in real-time (JSONL with --json)",
	Long: `Monitors all services and streams lifecycle events.
Use with --json for JSONL output (one JSON object per line).

Events: starting, alive, crashed, restarting, stopped, health, failed, gave_up`,
	Args: cobra.NoArgs,
	RunE: runWatch,
}

func init() {
	rootCmd.AddCommand(watchCmd)
}

func runWatch(cmd *cobra.Command, _ []string) error {
	var events *clikitstream.Writer
	if jsonMode {
		events = clikitstream.New(clikitcommand.StreamWriter(cmd))
	}
	failStream := func(err error) error {
		d := diag.FromError(err)
		_ = events.Fail(d)
		_ = events.Finish(diag.ExitCode(d))
		return result.StreamComplete(events.ExitCode())
	}
	if allProjects {
		if events != nil {
			return runFleetWatch(cmd, events)
		}
		return runFleetWatch(cmd, nil)
	}
	mgr, err := loadManager()
	if err != nil {
		if events != nil {
			return failStream(err)
		}
		return err
	}

	out := newOutput()
	if !jsonMode {
		out.Info("Watching services (Ctrl+C to stop)...")
	}

	// Start watch in background goroutine.
	ctx := cmd.Context()
	go mgr.Watch(ctx)

	// Read events.
	for {
		select {
		case <-ctx.Done():
			if events != nil {
				_ = events.Finish(diag.ExitOK)
				return result.StreamComplete(events.ExitCode())
			}
			return nil
		case event := <-mgr.Events():
			if jsonMode {
				if event.Error != "" {
					d := diag.FromError(diag.New("KB_SERVICE_UNAVAILABLE", "A watched service failed."))
					if err := events.Fail(d); err != nil {
						return failStream(err)
					}
				} else {
					if err := events.Event(event); err != nil {
						return failStream(err)
					}
				}
			} else {
				icon := out.StatusIcon(event.Event)
				msg := fmt.Sprintf("%s %s", event.Service, event.Event)
				if event.Elapsed != "" {
					msg += " (" + event.Elapsed + ")"
				}
				if event.Error != "" {
					msg += " — " + event.Error
				}
				clikitcommand.Printf("  %s %s\n", icon, msg)
			}
		}
	}
}

type fleetWatchEvent struct {
	Project string        `json:"project"`
	Event   manager.Event `json:"event"`
}

func runFleetWatch(cmd *cobra.Command, stream *clikitstream.Writer) error {
	failStream := func(err error) error {
		d := diag.FromError(err)
		_ = stream.Fail(d)
		_ = stream.Finish(diag.ExitCode(d))
		return result.StreamComplete(stream.ExitCode())
	}
	items, err := loadFleetManagers()
	if err != nil {
		if stream != nil {
			d := diag.FromError(err)
			_ = stream.Fail(d)
			_ = stream.Finish(diag.ExitCode(d))
			return result.StreamComplete(stream.ExitCode())
		}
		return err
	}
	out := newOutput()
	if !jsonMode {
		out.Info("Watching all projects (Ctrl+C to stop)...")
	}

	type taggedEvent struct {
		project string
		event   manager.Event
	}
	events := make(chan taggedEvent, 100)
	ctx := cmd.Context()
	var wg sync.WaitGroup
	for _, item := range items {
		if item.Mgr == nil {
			continue
		}
		wg.Add(1)
		go func(item fleetManager) {
			defer wg.Done()
			go item.Mgr.Watch(ctx)
			for {
				select {
				case <-ctx.Done():
					return
				case event := <-item.Mgr.Events():
					events <- taggedEvent{project: item.Alias, event: event}
				}
			}
		}(item)
	}
	go func() {
		wg.Wait()
		close(events)
	}()

	for tagged := range events {
		if jsonMode {
			value := fleetWatchEvent{Project: tagged.project, Event: tagged.event}
			if tagged.event.Error != "" {
				d := diag.FromError(diag.New("KB_SERVICE_UNAVAILABLE", "A watched service failed."))
				if err := stream.Fail(d); err != nil {
					return failStream(err)
				}
			} else {
				if err := stream.Event(value); err != nil {
					return failStream(err)
				}
			}
			continue
		}
		icon := out.StatusIcon(tagged.event.Event)
		msg := fmt.Sprintf("%s %s", tagged.project, tagged.event.Service)
		msg += " " + tagged.event.Event
		if tagged.event.Elapsed != "" {
			msg += " (" + tagged.event.Elapsed + ")"
		}
		if tagged.event.Error != "" {
			msg += " — " + tagged.event.Error
		}
		clikitcommand.Printf("  %s %s\n", icon, msg)
	}
	if stream != nil {
		_ = stream.Finish(diag.ExitOK)
		return result.StreamComplete(stream.ExitCode())
	}
	return nil
}
