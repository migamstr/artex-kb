package server

// advisor_runner.go wires the three Advisor modes into the Engine event loop.
// All three run asynchronously and never block the worker->planner notification.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"

	"github.com/Autumn-27/artex/advisor"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/knowledge"
	"github.com/Autumn-27/norma/llm"
)

// ── Mode 1: Review (跟进任务) ─────────────────────────────────────────────────

// triggerReview is called after a worker intent completes. Async, never blocks.
func (s *Server) triggerReview(ctx context.Context, t *Task, intentID int64) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[advisor/review] panic: %v", r)
			}
		}()
		s.runReview(ctx, t, intentID)
	}()
}

func (s *Server) runReview(ctx context.Context, t *Task, intentID int64) {
	adv := s.advisorForTask(t)
	if adv == nil {
		return
	}
	in, err := s.buildReviewInput(t, intentID)
	if err != nil {
		log.Printf("[advisor/review] build input task %s: %v", t.ID, err)
		return
	}
	res, err := adv.Review(ctx, in)
	if err != nil {
		log.Printf("[advisor/review] task %s intent #%d: %v", t.ID, intentID, err)
		return
	}
	if res == nil {
		return
	}
	if len(res.Items) > 0 {
		s.routeAdvisory(ctx, t, res.Items)
	}
	// Write armor entries (verified/negative) to the knowledge base.
	if len(res.Armor) > 0 && s.m.pg != nil {
		taskIDInt, _ := strconv.ParseInt(t.ID, 10, 64)
		for _, entry := range res.Armor {
			var writeErr error
			if entry.Status == "verified" {
				_, writeErr = s.m.pg.AddVerifiedDoc(taskIDInt, entry.Category, entry.Title, entry.Content)
			} else {
				_, writeErr = s.m.pg.AddNegativeDoc(taskIDInt, entry.Category, entry.Title, entry.Content)
			}
			if writeErr != nil {
				log.Printf("[advisor/review] write armor entry: %v", writeErr)
			}
		}
	}
}

func (s *Server) buildReviewInput(t *Task, intentID int64) (advisor.ReviewInput, error) {
	in := advisor.ReviewInput{IntentID: intentID}
	if id, err := strconv.ParseInt(t.ID, 10, 64); err == nil {
		in.TaskID = id
	}

	// intent summary
	if node, err := t.Store.GetNode(intentID); err == nil && node != nil {
		var p map[string]interface{}
		if json.Unmarshal(node.Payload, &p) == nil {
			if s, ok := p["summary"].(string); ok {
				in.Intent = s
			}
		}
	}

	// facts yielded by this intent
	if factIDs, err := t.Store.FactsYielded(intentID); err == nil {
		for _, fid := range factIDs {
			if n, err := t.Store.GetNode(fid); err == nil && n != nil {
				var p map[string]interface{}
				if json.Unmarshal(n.Payload, &p) == nil {
					if sum, ok := p["summary"].(string); ok {
						in.NewFacts = append(in.NewFacts, sum)
					}
				}
			}
		}
	}

	// findings yielded by this intent
	if findingIDs, err := t.Store.FindingsYieldedByIntent(intentID); err == nil {
		for _, fid := range findingIDs {
			if n, err := t.Store.GetNode(fid); err == nil && n != nil {
				var p map[string]interface{}
				if json.Unmarshal(n.Payload, &p) == nil {
					if nm, ok := p["name"].(string); ok {
						in.Findings = append(in.Findings, nm)
					}
				}
			}
		}
	}

	// assets anchored to this intent (for coverage-gap check)
	if s.m.assets != nil {
		if assetIDs, err := t.Store.AnchoredAssetIDs(intentID); err == nil && len(assetIDs) > 0 {
			if assets, err := s.m.assets.GetByIDs(assetIDs); err == nil {
				for _, a := range assets {
					switch {
					case len(a.Technologies) > 0:
						for _, tech := range a.Technologies {
							in.NewAssets = append(in.NewAssets, tech)
						}
					case a.URL != "":
						in.NewAssets = append(in.NewAssets, a.URL)
					case a.Domain != "":
						in.NewAssets = append(in.NewAssets, a.Domain)
					case a.IP != "":
						in.NewAssets = append(in.NewAssets, a.IP)
					}
				}
			}
		}
	}

	// task scope
	if s.m.assets != nil {
		if id, err := strconv.ParseInt(t.ID, 10, 64); err == nil {
			if scopes, err := s.m.assets.ListTaskScope(id); err == nil {
				for _, sc := range scopes {
					switch {
					case sc.Domain != "":
						in.Scope = append(in.Scope, sc.Domain)
					case sc.Net != "":
						in.Scope = append(in.Scope, sc.Net)
					case sc.Value != "":
						in.Scope = append(in.Scope, sc.Value)
					}
				}
			}
		}
	}
	return in, nil
}

// ── Mode 2: Summarize (总结经验) ──────────────────────────────────────────────

// triggerSummarize is called when a task transitions to done. Async.
func (s *Server) triggerSummarize(ctx context.Context, t *Task) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[advisor/summarize] panic: %v", r)
			}
		}()
		s.runSummarize(ctx, t)
	}()
}

func (s *Server) runSummarize(ctx context.Context, t *Task) {
	if s.m.pg == nil {
		return
	}
	prov := s.globalAdvisorProvider()
	if prov == nil {
		return
	}
	kb := knowledge.NewService(s.m.pg)
	adv := advisor.New(prov, s.m.NucleiIndex(), kb, s.m.pg)
	in := s.buildSummarizeInput(t)
	n, err := adv.Summarize(ctx, in)
	if err != nil {
		log.Printf("[advisor/summarize] task %s: %v", t.ID, err)
		return
	}
	if n > 0 {
		log.Printf("[advisor/summarize] task %s: %d lesson(s) written to knowledge base", t.ID, n)
	}
}

func (s *Server) buildSummarizeInput(t *Task) advisor.SummarizeInput {
	in := advisor.SummarizeInput{TaskGoal: t.Goal}
	if id, err := strconv.ParseInt(t.ID, 10, 64); err == nil {
		in.TaskID = id
	}
	if findings, err := t.Store.AllConfirmedFindings(); err == nil {
		for _, f := range findings {
			var p map[string]interface{}
			if json.Unmarshal(f.Payload, &p) == nil {
				nm, _ := p["name"].(string)
				sev, _ := p["severity"].(string)
				ev, _ := p["evidence"].(string)
				if nm != "" {
					in.Findings = append(in.Findings, advisor.SumFinding{
						Name: nm, Severity: sev, Evidence: ev,
					})
				}
			}
		}
	}
	return in
}

// ── Mode 3: Optimize (整体优化) ───────────────────────────────────────────────

// TriggerOptimize is called by the nuclei Syncer after a git pull.
func (s *Server) TriggerOptimize(ctx context.Context, newTags []string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[advisor/optimize] panic: %v", r)
			}
		}()
		s.runOptimize(ctx, newTags)
	}()
}

func (s *Server) runOptimize(ctx context.Context, newTags []string) {
	if s.m.pg == nil {
		return
	}
	prov := s.globalAdvisorProvider()
	if prov == nil {
		return
	}
	adv := advisor.New(prov, s.m.NucleiIndex(), nil, s.m.pg)
	in := advisor.OptimizeInput{NewTemplateTags: newTags}

	// recent tasks — use ListTasks and filter done
	if tasks, err := s.m.pg.ListTasks(); err == nil {
		count := 0
		for _, pt := range tasks {
			if count >= 10 {
				break
			}
			if pt.Status != "done" {
				continue
			}
			ot := advisor.OptimizeTask{TaskID: pt.ID, Goal: pt.Goal}
			// asset technologies
			if assets, err := s.m.assets.QueryByTask(pt.ID, "", 50, 0); err == nil {
				for _, a := range assets {
					for _, tech := range a.Technologies {
						if tech != "" {
							ot.Assets = append(ot.Assets, tech)
						}
					}
				}
			}
			// finding names
			taskIDStr := strconv.FormatInt(pt.ID, 10)
			ff, _, err := s.m.pg.ListFindingsPage(db.FindingFilter{TaskID: taskIDStr}, 1, 50)
			if err == nil {
				for _, f := range ff {
					ot.Findings = append(ot.Findings, f.Name)
				}
			}
			in.RecentTasks = append(in.RecentTasks, ot)
			count++
		}
	}

	res, err := adv.Optimize(ctx, in)
	if err != nil {
		log.Printf("[advisor/optimize] %v", err)
		return
	}
	if res == nil || len(res.Items) == 0 {
		return
	}
	for _, item := range res.Items {
		log.Printf("[advisor/optimize] [%s] %s — %s", item.Severity, item.Finding, item.Suggestion)
	}
}

// ── routing ───────────────────────────────────────────────────────────────────

// routeAdvisory dispatches advisory items to the appropriate channel.
//   nit     - add_hint, planner reads next round
//   concern - add_hint + immediate planner wake
//   blocker - high-priority activity record + hint + planner wake
func (s *Server) routeAdvisory(ctx context.Context, t *Task, items []advisor.Item) {
	for _, item := range items {
		msg := fmt.Sprintf("[Advisor/%s] %s — %s", item.Severity, item.Finding, item.Suggestion)
		switch item.Severity {
		case advisor.Nit:
			_ = t.Store.AddHint(msg)
		case advisor.Concern:
			_ = t.Store.AddHint(msg)
			t.Notify()
		case advisor.Blocker:
			s.engine.emitActivity(t, db.Activity{
				Worker:  "advisor",
				Kind:    "text",
				Summary: msg,
				IsError: true,
			})
			_ = t.Store.AddHint(msg)
			t.Notify()
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// advisorForTask returns an Advisor using the task's planner LLM provider.
func (s *Server) advisorForTask(t *Task) *advisor.Advisor {
	pl, _ := s.engine.snapshotFor(t)
	if pl == nil {
		return nil
	}
	if s.m.pg == nil {
		return nil
	}
	kb := knowledge.NewService(s.m.pg)
	return advisor.New(pl.Provider(), s.m.NucleiIndex(), kb, s.m.pg)
}

// globalAdvisorProvider returns the global LLM provider for task-independent modes.
func (s *Server) globalAdvisorProvider() llm.Provider {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	if !s.llmOn {
		return nil
	}
	return s.llmProv
}
