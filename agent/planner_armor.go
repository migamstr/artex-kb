package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// armorBlock returns the weapon-library injection block for the planner system
// prompt. Computed once per task run, cached in armorCache.
// Verified entries shown as ready-to-use; unverified as reference.
// Negative entries are excluded by SearchArmorDocs.
func (p *Planner) armorBlock(ctx context.Context, taskID int64, ts *db.ExplorationStore, goal string) string {
	expID := ts.ID()
	p.armorMu.Lock()
	if cached, ok := p.armorCache[expID]; ok {
		p.armorMu.Unlock()
		return cached
	}
	p.armorMu.Unlock()

	pg := ts.DB()
	if pg == nil {
		return ""
	}
	q := strings.TrimSpace(goal)
	if len([]rune(q)) > 120 {
		q = string([]rune(q)[:120])
	}
	docs, err := pg.SearchArmorDocs(q, taskID, 12)
	if err != nil || len(docs) == 0 {
		p.armorMu.Lock()
		p.armorCache[expID] = ""
		p.armorMu.Unlock()
		return ""
	}

	var verified, unverified []db.KnowledgeDoc
	for _, d := range docs {
		if d.Status == db.KBStatusVerified {
			verified = append(verified, d)
		} else {
			unverified = append(unverified, d)
		}
	}

	var b strings.Builder
	b.WriteString("\n\n【武器库 (知识库命中)】以下条目在当前目标相关历史任务中已记录，供本次规划参考：")
	if len(verified) > 0 {
		b.WriteString("\n\n✅ 已验证可用：")
		for i, d := range verified {
			fmt.Fprintf(&b, "\n%d. [%s] %s\n   %s", i+1, d.Category, d.Title, kbTruncate(d.Content, 300))
		}
	}
	if len(unverified) > 0 {
		b.WriteString("\n\n📋 参考条目（未验证）：")
		for i, d := range unverified {
			fmt.Fprintf(&b, "\n%d. [%s] %s\n   %s", i+1, d.Category, d.Title, kbTruncate(d.Content, 200))
		}
	}
	b.WriteString("\n\n以上条目仅供参考，以实际探测结果为准。")

	ids := make([]int64, len(docs))
	for i, d := range docs { ids[i] = d.ID }
	go pg.IncrKnowledgeHitCount(ids)

	block := b.String()
	p.armorMu.Lock()
	p.armorCache[expID] = block
	p.armorMu.Unlock()
	return block
}

func kbTruncate(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes { return s }
	return string(r[:maxRunes]) + "..."
}
