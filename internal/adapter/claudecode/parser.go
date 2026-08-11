package claudecode

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/haminh7036/memremark/internal/observation"
)

type rawEvent struct {
	Type      string      `json:"type"`
	Timestamp string      `json:"timestamp"`
	SessionID string      `json:"sessionId"`
	CWD       string      `json:"cwd"`
	Message   *rawMessage `json:"message"`
}

type rawMessage struct {
	Content json.RawMessage `json:"content"`
}

type rawContentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`          // present on tool_use
	Name      string          `json:"name"`        // present on tool_use
	Input     json.RawMessage `json:"input"`       // present on tool_use
	ToolUseID string          `json:"tool_use_id"` // present on tool_result
	Content   json.RawMessage `json:"content"`     // present on tool_result
	Text      string          `json:"text"`        // present on text blocks
}

type pendingToolUse struct {
	name      string
	input     string
	sessionID string
	cwd       string
	timestamp time.Time
}

// Parser turns a Claude Code transcript's JSONL lines into Observations
// by pairing each tool_use block with its matching tool_result. It is
// stateful across calls to Feed since the two halves of a tool call
// appear on different lines.
type Parser struct {
	pending map[string]pendingToolUse // keyed by tool_use id
}

// NewParser returns an empty Parser.
func NewParser() *Parser {
	return &Parser{pending: make(map[string]pendingToolUse)}
}

// Feed processes one JSONL line. It returns a completed Observation once
// a matching tool_result closes out a previously seen tool_use;
// otherwise ok is false. Malformed or empty lines are skipped
// (ok=false, err=nil) since a mid-write transcript can contain a
// truncated final line.
func (p *Parser) Feed(line []byte) (obs observation.Observation, ok bool, err error) {
	if len(line) == 0 {
		return observation.Observation{}, false, nil
	}

	var ev rawEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return observation.Observation{}, false, nil
	}
	if ev.Message == nil || len(ev.Message.Content) == 0 {
		return observation.Observation{}, false, nil
	}

	ts, _ := time.Parse(time.RFC3339, ev.Timestamp)

	var blocks []rawContentBlock
	if err := json.Unmarshal(ev.Message.Content, &blocks); err != nil {
		// content is a plain string (an ordinary text turn) -- nothing to pair.
		return observation.Observation{}, false, nil
	}

	switch ev.Type {
	case "assistant":
		for _, b := range blocks {
			if b.Type != "tool_use" {
				continue
			}
			p.pending[b.ID] = pendingToolUse{
				name:      b.Name,
				input:     string(b.Input),
				sessionID: ev.SessionID,
				cwd:       ev.CWD,
				timestamp: ts,
			}
		}
		return observation.Observation{}, false, nil

	case "user":
		for _, b := range blocks {
			if b.Type != "tool_result" {
				continue
			}
			pend, found := p.pending[b.ToolUseID]
			if !found {
				continue
			}
			delete(p.pending, b.ToolUseID)
			return observation.Observation{
				WingPath:  pend.cwd,
				SessionID: pend.sessionID,
				ToolName:  pend.name,
				Content:   fmt.Sprintf("input: %s\nresult: %s", pend.input, resultContentText(b.Content)),
				Timestamp: pend.timestamp,
			}, true, nil
		}
		return observation.Observation{}, false, nil
	}

	return observation.Observation{}, false, nil
}

// resultContentText extracts human-readable text from a tool_result's
// content field, which Claude Code emits either as a plain string or as
// an array of {"type":"text","text":"..."} blocks.
func resultContentText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []rawContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var out string
		for _, b := range blocks {
			if b.Type == "text" {
				out += b.Text
			}
		}
		return out
	}
	return string(raw)
}
