package advisor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/knowledge"
	"github.com/Autumn-27/artex/nuclei"
	"github.com/Autumn-27/norma/llm"
)

// Severity mirrors OMP Advisor levels.
type Severity string

const (
	Nit     Severity = "nit"
	Concern Severity = "concern"
	Blocker Severity = "blocker"
)

// Item is one advisory opinion.
type Item struct {
	Severity   Severity `json:"severity"`
	Finding    string   `json:"finding"`
	Suggestion string   `json:"suggestion"`
}

// Result is the structured output of one Advisor run.
type Result struct {
	Items []Item `json:"items"`
}

// Advisor holds shared resources for all three modes.
type Advisor struct {
	prov      llm.Provider
	nucleiIdx *nuclei.Index
	kb        *knowledge.Service
	pg        *db.DB
}

// New creates an Advisor. All fields are optional.
func New(prov llm.Provider, idx *nuclei.Index, kb *knowledge.Service, pg *db.DB) *Advisor {
	return &Advisor{prov: prov, nucleiIdx: idx, kb: kb, pg: pg}
}

// UpdateProvider replaces the LLM provider.
func (a *Advisor) UpdateProvider(prov llm.Provider) { a.prov = prov }

// ReviewInput is the context snapshot for Review mode (跟进任务).
type ReviewInput struct {
	TaskID    int64
	IntentID  int64
	Intent    string
	NewFacts  []string
	NewAssets []string
	Findings  []string
	Scope     []string
}

// Review runs a single LLM completion to advise on a just-finished worker run.
func (a *Advisor) Review(ctx context.Context, in ReviewInput) (*Result, error) {
	if a.prov == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	nucleiHint := ""
	if a.nucleiIdx != nil {
		tc, tg, _ := a.nucleiIdx.Stats()
		nucleiHint = fmt.Sprintf("nuclei-templates: %d templates, %d tags.", tc, tg)
	}
	req := llm.CompletionRequest{
		System:    []string{reviewSystemPrompt},
		Messages:  []llm.Message{llm.UserText(buildReviewInput(in, nucleiHint))},
		MaxTokens: 800,
		Thinking:  "disabled",
	}
	msg, _, _, err := a.prov.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("advisor review: %w", err)
	}
	return parseResult(msg.Text()), nil
}

// SummarizeInput is the task context for Summarize mode (总结经验).
type SummarizeInput struct {
	TaskID    int64
	TaskGoal  string
	Findings  []SumFinding
	KeyFacts  []string
	ToolsUsed []string
}

// SumFinding is a confirmed finding summary.
type SumFinding struct {
	Name     string
	Severity string
	Evidence string
}

// Summarize reads a completed task and writes a lesson to the knowledge base.
func (a *Advisor) Summarize(ctx context.Context, in SummarizeInput) (int, error) {
	if a.prov == nil || a.kb == nil {
		return 0, nil
	}
	if len(in.Findings) == 0 && len(in.KeyFacts) == 0 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req := llm.CompletionRequest{
		System:    []string{summarizeSystemPrompt},
		Messages:  []llm.Message{llm.UserText(buildSummarizeInput(in))},
		MaxTokens: 1200,
		Thinking:  "disabled",
	}
	msg, _, _, err := a.prov.Complete(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("advisor summarize: %w", err)
	}
	body := strings.TrimSpace(msg.Text())
	if body == "" {
		return 0, nil
	}
	title := fmt.Sprintf("task-%d-lesson", in.TaskID)
	if in.TaskGoal != "" {
		g := in.TaskGoal
		if len([]rune(g)) > 60 { g = string([]rune(g)[:60]) + "..." }
		title = fmt.Sprintf("task-%d: %s", in.TaskID, g)
	}
	docID, err := a.kb.PG().AddKnowledgeDoc(&db.KnowledgeDoc{
		Scope:    "global",
		Category: "task-lessons",
		Title:    title,
		Content:  body,
		Source:   "advisor",
	})
	if err != nil {
		return 0, fmt.Errorf("advisor summarize write: %w", err)
	}
	log.Printf("[advisor] summarize: task #%d -> doc #%d", in.TaskID, docID)
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if err := a.kb.IndexDoc(bgCtx, docID); err != nil {
			log.Printf("[advisor] index doc #%d: %v", docID, err)
		}
	}()
	return 1, nil
}

// OptimizeInput is the context for Optimize mode (整体优化).
type OptimizeInput struct {
	NewTemplateTags []string
	RecentTasks     []OptimizeTask
}

// OptimizeTask is one recent completed task summary.
type OptimizeTask struct {
	TaskID   int64
	Goal     string
	Assets   []string
	Findings []string
}

// OptimizeResult holds systemic suggestions.
type OptimizeResult struct{ Items []Item }

// Optimize cross-matches new nuclei tags against recent task assets.
func (a *Advisor) Optimize(ctx context.Context, in OptimizeInput) (*OptimizeResult, error) {
	if a.prov == nil { return nil, nil }
	if len(in.NewTemplateTags) == 0 && len(in.RecentTasks) == 0 { return &OptimizeResult{}, nil }
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	req := llm.CompletionRequest{
		System:    []string{optimizeSystemPrompt},
		Messages:  []llm.Message{llm.UserText(buildOptimizeInput(in))},
		MaxTokens: 1000,
		Thinking:  "disabled",
	}
	msg, _, _, err := a.prov.Complete(ctx, req)
	if err != nil { return nil, fmt.Errorf("advisor optimize: %w", err) }
	res := parseResult(msg.Text())
	if res == nil { return &OptimizeResult{}, nil }
	return &OptimizeResult{Items: res.Items}, nil
}

func buildReviewInput(in ReviewInput, nucleiHint string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## intent #%d\n%s\n\n", in.IntentID, in.Intent)
	if len(in.NewFacts) > 0 {
		b.WriteString("## new facts\n")
		for _, f := range in.NewFacts { fmt.Fprintf(&b, "- %s\n", f) }
	}
	if len(in.NewAssets) > 0 {
		b.WriteString("## new assets\n")
		for _, a := range in.NewAssets { fmt.Fprintf(&b, "- %s\n", a) }
	}
	if len(in.Findings) > 0 {
		b.WriteString("## findings\n")
		for _, f := range in.Findings { fmt.Fprintf(&b, "- %s\n", f) }
	}
	if len(in.Scope) > 0 {
		fmt.Fprintf(&b, "## scope\n%s\n\n", strings.Join(in.Scope, ", "))
	}
	if nucleiHint != "" { fmt.Fprintf(&b, "## tools\n%s\n", nucleiHint) }
	return b.String()
}

func buildSummarizeInput(in SummarizeInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## goal\n%s\n\n", in.TaskGoal)
	for _, f := range in.Findings {
		fmt.Fprintf(&b, "- [%s] %s\n  evidence: %s\n", f.Severity, f.Name, f.Evidence)
	}
	for _, f := range in.KeyFacts { fmt.Fprintf(&b, "- %s\n", f) }
	if len(in.ToolsUsed) > 0 {
		fmt.Fprintf(&b, "## tools used\n%s\n", strings.Join(in.ToolsUsed, ", "))
	}
	return b.String()
}

func buildOptimizeInput(in OptimizeInput) string {
	var b strings.Builder
	if len(in.NewTemplateTags) > 0 {
		fmt.Fprintf(&b, "## new nuclei tags\n%s\n\n", strings.Join(in.NewTemplateTags, ", "))
	}
	for _, t := range in.RecentTasks {
		fmt.Fprintf(&b, "### task #%d: %s\n", t.TaskID, t.Goal)
		if len(t.Assets) > 0 { fmt.Fprintf(&b, "assets: %s\n", strings.Join(t.Assets, ", ")) }
		if len(t.Findings) > 0 { fmt.Fprintf(&b, "findings: %s\n", strings.Join(t.Findings, ", ")) }
	}
	return b.String()
}

func parseResult(text string) *Result {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, ````json`); i >= 0 {
		text = text[i+7:]
		if j := strings.Index(text, `````); j >= 0 { text = text[:j] }
	} else if i := strings.Index(text, `````); i >= 0 {
		text = text[i+3:]
		if j := strings.Index(text, `````); j >= 0 { text = text[:j] }
	}
	text = strings.TrimSpace(text)
	var r Result
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		return &Result{}
	}
	return &r
}
