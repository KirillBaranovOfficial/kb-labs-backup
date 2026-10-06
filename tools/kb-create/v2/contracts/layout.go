package contracts

import (
	_ "embed"
	"encoding/json"
)

//go:embed platform-layout.json
var platformLayoutJSON []byte

type platformLayout struct {
	Schema            string   `json:"schema"`
	RuntimeStatePaths []string `json:"runtimeStatePaths"`
}

// RuntimeStatePaths returns the root-relative paths that a running host may
// mutate without changing the installed platform files.
func RuntimeStatePaths() []string {
	var layout platformLayout
	if err := json.Unmarshal(platformLayoutJSON, &layout); err != nil {
		panic("invalid embedded platform layout contract: " + err.Error())
	}
	return append([]string(nil), layout.RuntimeStatePaths...)
}
