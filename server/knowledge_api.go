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

// handleDeleteKnowledgeDoc DELETE /api/knowledge/docs/{id}
func (s *Server) handleDeleteKnowledgeDoc(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	if err := s.m.pg.DeleteKnowledgeDoc(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// handleUpdateKnowledgeDoc PUT /api/knowledge/docs/{id}
// Body: {"category":"...","title":"...","content":"..."}
func (s *Server) handleUpdateKnowledgeDoc(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var body struct {
		Category string `json:"category"`
		Title    string `json:"title"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.Title == "" || body.Content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "title and content required"})
		return
	}
	if err := s.m.pg.UpdateKnowledgeDoc(id, body.Category, body.Title, body.Content); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	// Re-index asynchronously so semantic search stays fresh.
	go func() {
		kb := knowledge.NewService(s.m.pg)
		bgCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if err := kb.IndexDoc(bgCtx, id); err != nil {
			log.Printf("[knowledge-api] re-index doc #%d: %v", id, err)
		}
	}()
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
}

// handleGetKnowledgeConfig GET /api/knowledge/config
func (s *Server) handleGetKnowledgeConfig(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}
	cfg, err := s.m.pg.GetKnowledgeConfig()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// handleSaveKnowledgeConfig PUT /api/knowledge/config
// Body: {"enabled":true,"base_url":"...","model":"...","api_key":"...","dim":1024}
// Empty api_key keeps the existing key.
func (s *Server) handleSaveKnowledgeConfig(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}
	var body struct {
		Enabled bool   `json:"enabled"`
		BaseURL string `json:"base_url"`
		Model   string `json:"model"`
		APIKey  string `json:"api_key"`
		Dim     int    `json:"dim"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := s.m.pg.SaveKnowledgeConfig(body.Enabled, body.BaseURL, body.Model, body.APIKey, body.Dim); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	cfg, _ := s.m.pg.GetKnowledgeConfig()
	writeJSON(w, http.StatusOK, cfg)
}

// handleReindexKnowledge POST /api/knowledge/reindex
// Rebuilds all vector chunks from scratch. Long-running; runs synchronously up to 5 min.
func (s *Server) handleReindexKnowledge(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}
	kb := knowledge.NewService(s.m.pg)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	n, err := kb.Reindex(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"indexed": n, "message": fmt.Sprintf("重建完成：%d 篇文档", n)})
}

// handleTestEmbedder POST /api/knowledge/config/test
// Verifies the configured embedder. Returns the actual vector dimension.
func (s *Server) handleTestEmbedder(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}
	kb := knowledge.NewService(s.m.pg)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	dim, err := kb.TestEmbedder(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "ok": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dim": dim})
}

// handleSaveFindingToKB POST /api/knowledge/from-finding/{id}
// Writes a confirmed finding into the weapon library as a verified knowledge doc.
// Idempotent: if a doc already exists for this finding, returns the existing doc id.
func (s *Server) handleSaveFindingToKB(w http.ResponseWriter, r *http.Request) {
	if s.m.pg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db unavailable"})
		return
	}
	findingID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	// Dedup: return existing doc if already saved.
	if existing, _ := s.m.pg.KnowledgeDocByFindingID(findingID); existing > 0 {
		writeJSON(w, http.StatusOK, map[string]any{"id": existing, "created": false, "message": "已存在"})
		return
	}
	f, err := s.m.pg.GetFinding(findingID)
	if err != nil || f == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "finding not found"})
		return
	}
	title := f.Name
	if title == "" {
		title = f.VulnClass
	}
	content := fmt.Sprintf("漏洞类型: %s\n严重级别: %s\n\n%s", f.VulnClass, f.Severity, f.Summary)
	if f.Evidence != "" {
		content += "\n\n证据:\n" + f.Evidence
	}
	var taskID int64
	if f.TaskID != nil {
		taskID = *f.TaskID
	}
	doc := &dbpkg.KnowledgeDoc{
		Scope:           "global",
		Category:        "auto-finding",
		Title:           fmt.Sprintf("[%s] %s", f.Severity, title),
		Content:         content,
		Source:          "finding",
		Status:          dbpkg.KBStatusVerified,
		SourceFindingID: &findingID,
	}
	if taskID > 0 {
		doc.SourceTaskID = &taskID
	}
	id, err := s.m.pg.AddKnowledgeDoc(doc)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	// Index asynchronously.
	go func() {
		kb := knowledge.NewService(s.m.pg)
		bgCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		_ = kb.IndexDoc(bgCtx, id)
	}()
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "created": true, "message": "已写入武器库"})
}
