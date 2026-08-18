package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/version"
)

type Server struct {
	store *storage.Store
	in    *bufio.Reader
	out   io.Writer
	mu    sync.Mutex
}

func NewServer(store *storage.Store, in io.Reader, out io.Writer) *Server {
	var reader *bufio.Reader
	if in != nil {
		reader = bufio.NewReader(in)
	}
	return &Server{
		store: store,
		in:    reader,
		out:   out,
	}
}

func (s *Server) Serve(ctx context.Context) error {
	if s.in == nil {
		return fmt.Errorf("mcp: reader is nil")
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := s.in.ReadBytes('\n')
		if len(line) > 0 {
			if handleErr := s.HandleLine(ctx, line); handleErr != nil {
				fmt.Fprintf(os.Stderr, "mcp: handle error: %v\n", handleErr)
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) HandleLine(ctx context.Context, line []byte) error {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}

	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return s.sendResponse(&Response{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error: &RPCError{
				Code:    CodeParseError,
				Message: fmt.Sprintf("Parse error: %v", err),
			},
		})
	}

	// Notifications: no ID or ID is null with notification method
	if len(req.ID) == 0 || (string(req.ID) == "null" && strings.HasPrefix(req.Method, "notifications/")) {
		return nil // No response for notifications
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(&req)
	case "notifications/initialized":
		return nil
	case "ping":
		return s.handlePing(&req)
	case "tools/list":
		return s.handleToolsList(&req)
	case "tools/call":
		return s.handleToolsCall(ctx, &req)
	default:
		return s.sendResponse(&Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    CodeMethodNotFound,
				Message: fmt.Sprintf("Method %q not found", req.Method),
			},
		})
	}
}

func (s *Server) sendResponse(resp *Response) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.out == nil {
		return nil
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.out, "%s\n", data)
	return err
}

func (s *Server) handleInitialize(req *Request) error {
	return s.sendResponse(&Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]interface{}{
				"name":    "memremark-mcp",
				"version": version.Version,
			},
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
		},
	})
}

func (s *Server) handlePing(req *Request) error {
	return s.sendResponse(&Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]interface{}{},
	})
}

func (s *Server) handleToolsList(req *Request) error {
	tools := []Tool{
		{
			Name:        "search_memory",
			Description: "Search through distilled summaries and verbatim observations in the Memory Palace.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"query":     {Type: "string", Description: "Keyword to match in memory content. If omitted, returns recent memories."},
					"hall":      {Type: "string", Description: "Filter by hall classification.", Enum: []string{"fact", "discovery", "preference", "advice", "event"}},
					"type":      {Type: "string", Description: "Filter by drawer type.", Enum: []string{"summary", "verbatim", "all"}},
					"wing_path": {Type: "string", Description: "Workspace path. Defaults to current directory."},
					"limit":     {Type: "integer", Description: "Max results to return (default: 10, max: 50)."},
				},
				AdditionalProperties: false,
			},
		},
		{
			Name:        "remember",
			Description: "Explicitly record a new distilled knowledge item into the Memory Palace immediately.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"content":   {Type: "string", Description: "The knowledge item or decision to record."},
					"hall":      {Type: "string", Description: "Knowledge classification.", Enum: []string{"fact", "discovery", "preference", "advice"}},
					"wing_path": {Type: "string", Description: "Workspace path. Defaults to current directory."},
				},
				Required:             []string{"content", "hall"},
				AdditionalProperties: false,
			},
		},
		{
			Name:        "get_timeline",
			Description: "Retrieve chronological sequence of events and summaries for a session or time window.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"session_id": {Type: "string", Description: "Specific session ID to inspect."},
					"wing_path":  {Type: "string", Description: "Workspace path. Defaults to current directory."},
					"since":      {Type: "integer", Description: "Unix timestamp in seconds to fetch events after."},
					"limit":      {Type: "integer", Description: "Max entries to return (default: 20, max: 100)."},
				},
				AdditionalProperties: false,
			},
		},
		{
			Name:        "forget_memory",
			Description: "Delete an outdated or incorrect memory drawer by its ID.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"id": {Type: "integer", Description: "ID of the drawer to delete."},
				},
				Required:             []string{"id"},
				AdditionalProperties: false,
			},
		},
	}

	return s.sendResponse(&Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"tools": tools,
		},
	})
}

func (s *Server) handleToolsCall(ctx context.Context, req *Request) error {
	var callParams struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &callParams); err != nil {
		return s.sendResponse(&Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("Invalid params: %v", err),
			},
		})
	}

	var result ToolCallResult
	switch callParams.Name {
	case "search_memory":
		result = s.callSearchMemory(callParams.Arguments)
	case "remember":
		result = s.callRemember(callParams.Arguments)
	case "get_timeline":
		result = s.callGetTimeline(callParams.Arguments)
	case "forget_memory":
		result = s.callForgetMemory(callParams.Arguments)
	default:
		result = ToolCallResult{
			IsError: true,
			Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Unknown tool %q", callParams.Name)}},
		}
	}

	return s.sendResponse(&Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	})
}

func normalizePath(pathStr string) string {
	abs, err := filepath.Abs(pathStr)
	if err == nil {
		return abs
	}
	return filepath.Clean(pathStr)
}

func (s *Server) callSearchMemory(args map[string]interface{}) ToolCallResult {
	query, _ := args["query"].(string)
	hall, _ := args["hall"].(string)
	drawerType, _ := args["type"].(string)
	wingPath, _ := args["wing_path"].(string)
	limit := 10
	if l, ok := args["limit"].(float64); ok && int(l) > 0 {
		limit = int(l)
	}

	cleanPath := normalizePath(wingPath)
	wingID, err := s.store.GetOrCreateWing(cleanPath)
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Failed to resolve wing: %v", err)}}}
	}

	drawers, err := s.store.SearchDrawers(wingID, query, hall, drawerType, limit)
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Search error: %v", err)}}}
	}

	if len(drawers) == 0 {
		return ToolCallResult{Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("No memories found for wing %q.", cleanPath)}}}
	}

	var sb bytes.Buffer
	fmt.Fprintf(&sb, "Found %d memories in wing %q:\n", len(drawers), cleanPath)
	for _, d := range drawers {
		if d.ToolName != "" {
			fmt.Fprintf(&sb, "- [ID: %d] [%s:%s] %s (%s)\n", d.ID, d.Hall, d.ToolName, d.Content, d.CreatedAt.Format("2006-01-02 15:04:05"))
		} else {
			fmt.Fprintf(&sb, "- [ID: %d] [%s] %s (%s)\n", d.ID, d.Hall, d.Content, d.CreatedAt.Format("2006-01-02 15:04:05"))
		}
	}

	return ToolCallResult{Content: []ToolCallContent{{Type: "text", Text: sb.String()}}}
}

func (s *Server) callRemember(args map[string]interface{}) ToolCallResult {
	content, _ := args["content"].(string)
	hall, _ := args["hall"].(string)
	wingPath, _ := args["wing_path"].(string)

	if strings.TrimSpace(content) == "" {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: "Missing required argument 'content'"}}}
	}
	if strings.TrimSpace(hall) == "" {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: "Missing required argument 'hall'"}}}
	}

	cleanPath := normalizePath(wingPath)
	wingID, err := s.store.GetOrCreateWing(cleanPath)
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Failed to resolve wing: %v", err)}}}
	}

	id, err := s.store.InsertManualSummary(wingID, hall, content, time.Now())
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Failed to store memory: %v", err)}}}
	}

	return ToolCallResult{
		Content: []ToolCallContent{{
			Type: "text",
			Text: fmt.Sprintf("Successfully recorded memory [ID: %d] into hall %q for wing %q.", id, hall, cleanPath),
		}},
	}
}

func (s *Server) callGetTimeline(args map[string]interface{}) ToolCallResult {
	sessionID, _ := args["session_id"].(string)
	wingPath, _ := args["wing_path"].(string)
	limit := 20
	if l, ok := args["limit"].(float64); ok && int(l) > 0 {
		limit = int(l)
	}
	var since time.Time
	if sVal, ok := args["since"].(float64); ok && int64(sVal) > 0 {
		since = time.Unix(int64(sVal), 0)
	}

	cleanPath := normalizePath(wingPath)
	wingID, err := s.store.GetOrCreateWing(cleanPath)
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Failed to resolve wing: %v", err)}}}
	}

	timeline, err := s.store.GetTimeline(wingID, sessionID, since, limit)
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Failed to get timeline: %v", err)}}}
	}

	if len(timeline) == 0 {
		return ToolCallResult{Content: []ToolCallContent{{Type: "text", Text: "No events found for the specified timeline criteria."}}}
	}

	var sb bytes.Buffer
	fmt.Fprintf(&sb, "Timeline for wing %q (%d events):\n", cleanPath, len(timeline))
	for i, d := range timeline {
		timeStr := d.CreatedAt.Format("15:04:05")
		if d.ToolName != "" {
			fmt.Fprintf(&sb, "%d. [%s] [verbatim:%s] %s\n", i+1, timeStr, d.ToolName, d.Content)
		} else {
			fmt.Fprintf(&sb, "%d. [%s] [summary:%s] %s\n", i+1, timeStr, d.Hall, d.Content)
		}
	}

	return ToolCallResult{Content: []ToolCallContent{{Type: "text", Text: sb.String()}}}
}

func (s *Server) callForgetMemory(args map[string]interface{}) ToolCallResult {
	idVal, ok := args["id"].(float64)
	if !ok || int64(idVal) <= 0 {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: "Argument 'id' must be a positive integer"}}}
	}

	deleted, err := s.store.DeleteDrawer(int64(idVal))
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Failed to delete memory: %v", err)}}}
	}
	if !deleted {
		return ToolCallResult{Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Memory drawer [ID: %d] was not found.", int64(idVal))}}}
	}

	return ToolCallResult{Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Memory drawer [ID: %d] has been deleted.", int64(idVal))}}}
}
