package ui

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

// DrawerItem represents a timeline entry in the REST API response.
type DrawerItem struct {
	ID         int64     `json:"id"`
	WingID     int64     `json:"wing_id"`
	WingName   string    `json:"wing_name,omitempty"`
	WingPath   string    `json:"wing_path,omitempty"`
	Type       string    `json:"type"`
	Hall       string    `json:"hall"`
	ToolName   string    `json:"tool_name,omitempty"`
	Content    string    `json:"content"`
	SessionID  string    `json:"session_id,omitempty"`
	CoversFrom int64     `json:"covers_from,omitempty"`
	CoversTo   int64     `json:"covers_to,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) handleWings(w http.ResponseWriter, r *http.Request) {
	wings, err := s.store.ListWingsWithStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if wings == nil {
		wings = []storage.WingStats{}
	}
	writeJSON(w, http.StatusOK, wings)
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	wingID, _ := strconv.ParseInt(q.Get("wing_id"), 10, 64)
	hall := q.Get("hall")
	drawerType := q.Get("type")
	query := q.Get("q")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}

	drawers, err := s.store.SearchDrawers(wingID, query, hall, drawerType, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	items := make([]DrawerItem, 0, len(drawers))
	for _, d := range drawers {
		itemType := d.Type
		if itemType == "" {
			if d.ToolName != "" || d.Hall == "event" {
				itemType = "verbatim"
			} else {
				itemType = "summary"
			}
		}
		items = append(items, DrawerItem{
			ID:         d.ID,
			WingID:     d.WingID,
			Type:       itemType,
			Hall:       d.Hall,
			ToolName:   d.ToolName,
			Content:    d.Content,
			SessionID:  d.SessionID,
			CoversFrom: d.CoversFrom,
			CoversTo:   d.CoversTo,
			CreatedAt:  d.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetGlobalStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
