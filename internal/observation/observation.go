package observation

import "time"

// Observation is one normalized tool-use event captured from a CLI's own
// transcript, independent of which CLI produced it.
type Observation struct {
	WingPath  string // absolute project directory the observation belongs to
	SessionID string
	ToolName  string // empty for Antigravity CLI observations in v1 -- see reader.go
	Content   string // human-readable summary of the tool call and its result
	Timestamp time.Time
}
