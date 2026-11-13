package nuclei

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

const syncInterval = 24 * time.Hour

type Syncer struct {
	idx      *Index
	pg       *db.DB
	onUpdate func(newTags []string) // called after each successful pull+rebuild
}

// SetOnUpdate registers a callback invoked after each successful pull.
// newTags contains the tag names that were added in this pull (best-effort diff).
func (s *Syncer) SetOnUpdate(fn func([]string)) { s.onUpdate = fn }

func NewSyncer(idx *Index) *Syncer { return &Syncer{idx: idx} }

// SetDB wires a database connection so the syncer can ingest new CVE entries.
func (s *Syncer) SetDB(pg *db.DB) { s.pg = pg }

func (s *Syncer) Run(ctx context.Context) {
	next := nextMidnight()
	log.Printf("[nuclei-sync] first sync at %s", next.Format("2006-01-02 15:04:05"))
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			s.PullNow()
			next = next.Add(syncInterval)
		}
	}
}

func (s *Syncer) PullNow() error {
	dir := s.idx.Dir()
	log.Printf("[nuclei-sync] git pull %s", dir)
	cmd := exec.Command("git", "-C", dir, "pull", "--ff-only", "--quiet")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[nuclei-sync] pull failed: %v\n%s", err, out)
		return err
	}
	// Snapshot tag set before rebuild to compute the diff.
	before := make(map[string]struct{}, len(s.idx.Tags()))
	for _, t := range s.idx.Tags() {
		before[t] = struct{}{}
	}
	s.idx.Rebuild()
	// Ingest new CVE template entries into the knowledge base.
	if s.pg != nil {
		go s.IngestNewCVEs()
	}
	newCount, tg, at := s.idx.Stats()
	log.Printf("[nuclei-sync] index ready: %d templates %d tags (%s)", newCount, tg, at.Format(time.RFC3339))
	if s.onUpdate != nil {
		var added []string
		for _, t := range s.idx.Tags() {
			if _, existed := before[t]; !existed {
				added = append(added, t)
			}
		}
		s.onUpdate(added)
	}
	return nil
}

// IngestNewCVEs walks all CVE-tagged templates and bulk-inserts new entries
// into knowledge_docs. Uses BulkIngestCVEDocs: one existence-check query +
// batched INSERT ON CONFLICT DO NOTHING, replacing the previous N+1 approach.
func (s *Syncer) IngestNewCVEs() {
	if s.pg == nil {
		return
	}
	entries := s.idx.AllEntries()
	var rows []db.CVEDocRow
	for _, e := range entries {
		if !hasCVETag(e.Tags) {
			continue
		}
		title := e.Name
		if title == "" {
			title = e.ID
		}
		var parts []string
		if e.Description != "" {
			parts = append(parts, e.Description)
		}
		parts = append(parts, fmt.Sprintf("severity: %s", e.Severity))
		parts = append(parts, fmt.Sprintf("tags: %s", strings.Join(e.Tags, ", ")))
		parts = append(parts, fmt.Sprintf("template: %s", e.ID))
		rows = append(rows, db.CVEDocRow{Title: title, Content: strings.Join(parts, "\n")})
	}
	inserted, skipped, err := s.pg.BulkIngestCVEDocs(rows)
	if err != nil {
		log.Printf("[nuclei-sync] CVE ingest error: %v", err)
		return
	}
	log.Printf("[nuclei-sync] CVE ingest: %d inserted, %d already existed", inserted, skipped)
}
// hasCVETag reports whether the entry has a "cve" tag.
func hasCVETag(tags []string) bool {
	for _, t := range tags {
		if t == "cve" {
			return true
		}
	}
	return false
}

func nextMidnight() time.Time {
	now := time.Now()
	y, m, d := now.Date()
	return time.Date(y, m, d+1, 0, 5, 0, 0, now.Location())
}
