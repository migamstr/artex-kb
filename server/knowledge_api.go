package server

import (
	"context"
	"encoding/json"
	"time"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	dbpkg "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/knowledge"
)

// handleListKnowledgeDocs GET /api/knowledge/docs
func (s *Server) handleListKnowledgeDocs(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}
	scope := r.URL.Query().Get("scope") // global | task | (empty=all)
	category := r.URL.Query().Get("category")
	status := r.URL.Query().Get("status")
	var taskID int64
	if tid := r.URL.Query().Get("task_id"); tid != "" {
		taskID, _ = strconv.ParseInt(tid, 10, 64)
	}
	docs, err := s.m.pg.ListKnowledgeDocs(scope, taskID, category, status)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"docs": docs, "count": len(docs)})
}

// handleCreateKnowledgeDoc POST /api/knowledge/docs
// Accepts JSON body or multipart file upload.
// JSON: {"scope":"global","category":"cve-nuclei","title":"...","content":"...","status":"unverified"}
// Multipart: file field "file" + form fields scope/category/title/status.
func (s *Server) handleCreateKnowledgeDoc(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}

	var doc dbpkg.KnowledgeDoc

	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "multipart/form-data") {
		// File upload path
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "parse multipart: " + err.Error()})
			return
		}
		doc.Scope = r.FormValue("scope")
		doc.Category = r.FormValue("category")
		doc.Title = r.FormValue("title")
		doc.Status = r.FormValue("status")
		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "file field required"})
			return
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, 10<<20)) // 10 MB cap
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "read file: " + err.Error()})
			return
		}
		text, err := knowledge.ExtractText(header.Filename, raw)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "extract text: " + err.Error()})
			return
		}
		doc.Content = text
		doc.FileName = header.Filename
		doc.Bytes = int64(len(raw))
		doc.Source = "upload"
	} else {
		// JSON body path
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "decode json: " + err.Error()})
			return
		}
		doc.Source = "manual"
	}

	// Defaults
	if doc.Scope == "" { doc.Scope = "global" }
	if doc.Status == "" { doc.Status = db.KBStatusUnverified }
	if doc.Title == "" || doc.Content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "title and content required"})
		return
	}
	if !dbpkg.IsValidKBStatus(doc.Status) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "status must be unverified|verified|negative"})
		return
	}

	id, err := s.m.pg.AddKnowledgeDoc(&doc)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// Index asynchronously if KB service available.
	go func() {
		kb := knowledge.NewService(s.m.pg)
		bgCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if err := kb.IndexDoc(bgCtx, id); err != nil {
			log.Printf("[knowledge-api] index doc #%d: %v", id, err)
		}
	}()

	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "message": "created"})
}

// handleUpdateKnowledgeDocStatus PATCH /api/knowledge/docs/{id}/status
// Body: {"status":"verified"} — allows manual verification or marking negative.
func (s *Server) handleUpdateKnowledgeDocStatus(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !dbpkg.IsValidKBStatus(body.Status) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "status must be unverified|verified|negative"})
		return
	}
	if err := s.m.pg.SetKnowledgeDocStatus(id, body.Status); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": body.Status})
}

// handleSearchKnowledgeDocs GET /api/knowledge/docs/search?q=shiro&task_id=1&limit=10
func (s *Server) handleSearchKnowledgeDocs(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}
	q := r.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "q required"})
		return
	}
	var taskID int64
	if tid := r.URL.Query().Get("task_id"); tid != "" {
		taskID, _ = strconv.ParseInt(tid, 10, 64)
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	kb := knowledge.NewService(s.m.pg)
	searchCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	hits, mode, err := kb.Search(searchCtx, q, taskID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits, "count": len(hits), "query": q, "mode": mode})
}

// handleKnowledgeStats GET /api/knowledge/stats
func (s *Server) handleKnowledgeStats(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}
	global, task, err := s.m.pg.KnowledgeDocCount()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	semanticOK := s.m.pg.KnowledgeVectorAvailable()
	statusMap, err := s.m.pg.KnowledgeStatusCounts()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	type statusCount struct {
		Status string `json:"Status"`
		Count  int    `json:"Count"`
	}
	var bystatus []statusCount
	for s, c := range statusMap {
		bystatus = append(bystatus, statusCount{Status: s, Count: c})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"global_docs":    global,
		"task_docs":      task,
		"semantic_ready": semanticOK,
		"by_status":      bystatus,
	})
}

// handleImportFromTask POST /api/knowledge/import-task/{task_id}
// Imports findings and key facts from a completed task into the knowledge base.
func (s *Server) handleImportFromTask(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}
	taskIDStr := r.PathValue("task_id")
	taskID, err := strconv.ParseInt(taskIDStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid task_id"})
		return
	}
	// get task exploration
	pt, err := s.m.pg.GetTask(taskID)
	if err != nil || pt == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "task not found"})
		return
	}
	ts := s.m.pg.Exploration(pt.ExplorationID)
	findings, err := ts.AllConfirmedFindings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	count := 0
	for _, f := range findings {
		var p map[string]any
		if err := json.Unmarshal(f.Payload, &p); err != nil {
			continue
		}
		nm, _ := p["name"].(string)
		ev, _ := p["evidence"].(string)
		if nm == "" { continue }
		content := fmt.Sprintf("任务 #%d 确认漏洞\n证据：%s", taskID, ev)
		_, err := s.m.pg.AddVerifiedDoc(taskID, "auto-finding", nm, content)
		if err == nil { count++ }
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task_id": taskID,
		"imported": count,
		"message": fmt.Sprintf("imported %d findings from task #%d", count, taskID),
	})
}
