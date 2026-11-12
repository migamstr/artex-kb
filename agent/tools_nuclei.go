package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Autumn-27/artex/nuclei"
	actool "github.com/Autumn-27/norma/tool"
)

func (t *ToolSet) SetNucleiIndex(idx *nuclei.Index) { t.nucleiIdx = idx }

func (t *ToolSet) matchNucleiTemplates() actool.CoreTool {
	return readTool("match_nuclei_templates",
		"根据组件名称或指纹标签，从本地 nuclei-templates 索引中查找匹配的模板路径列表。"+
			"仅在资产有明确组件指纹（框架名、CMS、中间件版本）时调用；无指纹时不要调用。"+
			"返回模板的相对路径，可直接传给 run_nuclei。",
		obj(map[string]any{
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
				"description": "组件/指纹标签列表，例如 [\"shiro\",\"apache\"] 或 [\"cve-2021-44228\"]，全部小写",
			},
			"severity": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
				"description": "严重等级过滤，例如 [\"critical\",\"high\"]；留空返回全部等级",
			},
		}, "tags"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Tags     []string `json:"tags"`
				Severity []string `json:"severity"`
			}
			_ = json.Unmarshal(in, &a)
			if t.nucleiIdx == nil {
				return actool.Text("nuclei 索引未启用（nuclei-templates 目录未配置）。"), nil
			}
			if len(a.Tags) == 0 {
				return actool.Errorf("tags 不能为空"), nil
			}
			hits := t.nucleiIdx.Search(a.Tags, a.Severity)
			if len(hits) == 0 {
				return actool.Text(fmt.Sprintf(
					"未找到匹配 tags=%v severity=%v 的模板。可尝试更宽泛标签，或直接使用 nuclei -tags 参数。",
					a.Tags, a.Severity)), nil
			}
			tc, _, _ := t.nucleiIdx.Stats()
			var b strings.Builder
			fmt.Fprintf(&b, "找到 %d 个匹配模板（索引共 %d 个）：\n\n", len(hits), tc)
			for _, h := range hits {
				fmt.Fprintf(&b, "- [%s] %s  (severity: %s, tags: %s)\n",
					h.ID, h.Path, h.Severity, strings.Join(h.Tags, ","))
			}
			b.WriteString("\n模板根目录：" + t.nucleiIdx.Dir())
			return actool.Text(b.String()), nil
		})
}

type nucleiHit struct {
	TemplateID string `json:"template-id"`
	Info       struct {
		Name     string `json:"name"`
		Severity string `json:"severity"`
	} `json:"info"`
	MatchedAt string `json:"matched-at"`
}

func (t *ToolSet) runNuclei() actool.CoreTool {
	return readTool("run_nuclei",
		"对目标执行 nuclei 扫描，解析 JSON 输出，返回结构化结果摘要。"+
			"扫描完成后由 worker 判断哪些结果可信，再调用 report_finding 上报。"+
			"templates 传 match_nuclei_templates 返回的相对路径，或传空列表使用 tags 参数批量扫描。",
		obj(map[string]any{
			"target": str("目标 URL 或 IP，例如 https://192.168.1.1"),
			"templates": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
				"description": "模板相对路径列表（来自 match_nuclei_templates）；与 tags 二选一",
			},
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
				"description": "直接传 nuclei -tags 参数；与 templates 二选一",
			},
			"severity": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
				"description": "只跑指定等级，例如 [\"critical\",\"high\"]；留空=全部",
			},
		}, "target"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Target    string   `json:"target"`
				Templates []string `json:"templates"`
				Tags      []string `json:"tags"`
				Severity  []string `json:"severity"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Target) == "" {
				return actool.Errorf("target 不能为空"), nil
			}
			if len(a.Templates) == 0 && len(a.Tags) == 0 {
				return actool.Errorf("templates 或 tags 至少提供一个"), nil
			}
			args := []string{"-u", a.Target, "-j", "-silent", "-nc"}
			if t.nucleiIdx != nil {
				for _, rel := range a.Templates {
					args = append(args, "-t", t.nucleiIdx.AbsPath(rel))
				}
			} else {
				for _, p := range a.Templates {
					args = append(args, "-t", p)
				}
			}
			if len(a.Tags) > 0 {
				args = append(args, "-tags", strings.Join(a.Tags, ","))
			}
			if len(a.Severity) > 0 {
				args = append(args, "-severity", strings.Join(a.Severity, ","))
			}
			cmd := exec.CommandContext(ctx, "nuclei", args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			_ = cmd.Run()
			var hits []nucleiHit
			for _, line := range strings.Split(stdout.String(), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || !strings.HasPrefix(line, "{") {
					continue
				}
				var h nucleiHit
				if err := json.Unmarshal([]byte(line), &h); err == nil && h.TemplateID != "" {
					hits = append(hits, h)
				}
			}
			if len(hits) == 0 {
				msg := "nuclei 扫描完成，未发现漏洞。"
				if s := strings.TrimSpace(stderr.String()); s != "" {
					if len(s) > 400 {
						s = s[:400] + "…"
					}
					msg += "\nnuclei stderr: " + s
				}
				return actool.Text(msg), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "nuclei 扫描完成，发现 %d 个潜在漏洞（请验证后再调 report_finding）：\n\n", len(hits))
			for i, h := range hits {
				fmt.Fprintf(&b, "%d. [%s] %s\n   匹配目标: %s  |  严重等级: %s\n\n",
					i+1, h.TemplateID, h.Info.Name, h.MatchedAt, h.Info.Severity)
			}
			return actool.Text(b.String()), nil
		})
}
