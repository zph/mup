package supervisor

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ProcessState is a supervisor process state, independent of how the
// supervisord build spells it ("Running" vs "RUNNING").
// LOCAL-011
type ProcessState int

const (
	StateUnknown ProcessState = iota
	StateStopped
	StateStarting
	StateRunning
	StateBackoff
	StateStopping
	StateExited
	StateFatal
)

var processStateNames = map[ProcessState]string{
	StateUnknown:  "UNKNOWN",
	StateStopped:  "STOPPED",
	StateStarting: "STARTING",
	StateRunning:  "RUNNING",
	StateBackoff:  "BACKOFF",
	StateStopping: "STOPPING",
	StateExited:   "EXITED",
	StateFatal:    "FATAL",
}

// String returns the canonical upper-case name of the state.
func (s ProcessState) String() string {
	if name, ok := processStateNames[s]; ok {
		return name
	}
	return processStateNames[StateUnknown]
}

// IsRunning reports whether the process is up.
func (s ProcessState) IsRunning() bool { return s == StateRunning }

// IsFailed reports whether the process stopped on its own and is not coming back
// without intervention (or is crash-looping).
func (s ProcessState) IsFailed() bool {
	return s == StateFatal || s == StateExited || s == StateBackoff
}

// ParseProcessState maps a supervisor state string to a ProcessState,
// ignoring case.
func ParseProcessState(raw string) ProcessState {
	upper := strings.ToUpper(strings.TrimSpace(raw))
	for state, name := range processStateNames {
		if name == upper {
			return state
		}
	}
	return StateUnknown
}

// ProcessState returns the parsed form of the raw State string.
func (p *ProcessStatus) ProcessState() ProcessState {
	return ParseProcessState(p.State)
}

var ansiEscape = regexp.MustCompile(`\x1b?\[[0-9;]*m`)

const statusPIDField = "pid"

// parseStatusLine parses one line of `supervisord ctl status` output:
//
//	progname  STATE  pid 12345, uptime 0:01:23
func parseStatusLine(line string) (*ProcessStatus, error) {
	line = strings.TrimSpace(ansiEscape.ReplaceAllString(line, ""))

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil, fmt.Errorf("unexpected status output format: %s", line)
	}

	status := &ProcessStatus{Name: fields[0], State: fields[1]}

	for i, field := range fields {
		if field == statusPIDField && i+1 < len(fields) {
			if pid, err := strconv.Atoi(strings.TrimSuffix(fields[i+1], ",")); err == nil {
				status.PID = pid
			}
			break
		}
	}

	if len(fields) > 2 {
		status.Description = strings.Join(fields[2:], " ")
	}

	return status, nil
}
