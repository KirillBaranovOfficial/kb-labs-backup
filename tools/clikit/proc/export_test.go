//go:build darwin || linux

package proc

import "os"

// RunRecordFile is the file the record placement owner holds for runID now:
// the live record while the run is in flight, the finished record after it.
func RunRecordFile(stateDir, runID string) string {
	if _, err := os.Stat(recordPath(stateDir, runID, false)); err == nil {
		return recordPath(stateDir, runID, false)
	}
	return recordPath(stateDir, runID, true)
}
