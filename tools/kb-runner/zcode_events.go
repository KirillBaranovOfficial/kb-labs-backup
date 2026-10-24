//go:build darwin || linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
)

func parseZcodeEvent(line []byte) (VendorEvent, error) {
	var row struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
		Response  string `json:"response"`
		Payload   struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"payload"`
		Usage struct {
			Input  uint64 `json:"inputTokens"`
			Output uint64 `json:"outputTokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(line, &row); err != nil {
		return VendorEvent{}, fmt.Errorf("decode ZCode stream event: %w", err)
	}
	if row.Type == "" {
		return VendorEvent{}, errors.New("ZCode stream event is missing its type")
	}
	event := VendorEvent{RawType: row.Type, SessionID: row.SessionID, Kind: EventActivity}
	switch row.Type {
	case "session.created", "session.resumed":
		event.Kind = EventStarted
	case "turn.failed":
		event.Kind = EventVendorError
		event.Message = row.Payload.Error.Message
		if event.Message == "" {
			event.Message = "ZCode turn failed without an error message"
		}
	case "result":
		event.Kind = EventFinal
		event.FinalMessage = row.Response
		event.InputTokens = row.Usage.Input
		event.OutputTokens = row.Usage.Output
		event.Tokens = event.InputTokens + event.OutputTokens
	}
	return event, nil
}

func zcodeArgs(request AgentRequest, resume bool) []string {
	args := make([]string, 0, 13)
	if resume {
		args = append(args, "--resume", request.SessionID)
	}
	args = append(args,
		"--prompt", request.Prompt,
		"--cwd", request.Workdir,
		"--mode", "yolo",
		"--disallowed-tools", "AskUserQuestion EnterPlanMode ExitPlanMode",
		"--output-format", "stream-json",
		"--no-color",
	)
	return args
}
