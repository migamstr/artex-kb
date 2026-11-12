package server

import (
	"net/http"
	"time"
)

// handleNucleiStatus GET /api/nuclei/status
func (s *Server) handleNucleiStatus(w http.ResponseWriter, r *http.Request) {
	idx := s.m.NucleiIndex()
	if idx == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"available": false,
			"message":   "nuclei index not initialised",
		})
		return
	}
	tc, tg, at := idx.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"available":      true,
		"templates_dir":  idx.Dir(),
		"template_count": tc,
		"tag_count":      tg,
		"last_built":     at.Format(time.RFC3339),
	})
}

// handleNucleiSync POST /api/nuclei/sync — triggers immediate git pull + rebuild.
func (s *Server) handleNucleiSync(w http.ResponseWriter, r *http.Request) {
	if s.m.nucleiSyncer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "nuclei syncer not initialised"})
		return
	}
	go func() { _ = s.m.nucleiSyncer.PullNow() }()
	writeJSON(w, http.StatusAccepted, map[string]any{"message": "sync started in background"})
}
