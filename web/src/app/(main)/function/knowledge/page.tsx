"use client";
import * as React from "react";

import {
  BookMarkedIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  FilterXIcon,
  PencilIcon,
  PlusIcon,
  RefreshCwIcon,
  SearchIcon,
  SettingsIcon,
  ShieldOffIcon,
  SparklesIcon,
  Trash2Icon,
  XCircleIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { KBStatus, KnowledgeConfig, KnowledgeDoc, KnowledgeStats } from "@/lib/types";
import { cn } from "@/lib/utils";

// ---- constants ----

const STATUS_LABELS: Record<KBStatus, { label: string; cls: string }> = {
  verified:   { label: "已验证", cls: "bg-green-500/10 text-green-600 border-green-500/30" },
  unverified: { label: "未验证", cls: "bg-yellow-500/10 text-yellow-600 border-yellow-500/30" },
  negative:   { label: "负面",   cls: "bg-red-500/10 text-red-600 border-red-500/30" },
};

const CATEGORY_LABELS: Record<string, string> = {
  "cve-nuclei":       "CVE情报",
  "auto-finding":     "自动发现",
  "auto-negative":    "无效路径",
  "nuclei-optimize":  "模板优化",
  "task-lessons":     "任务经验",
  "rsi-lesson":       "RSI经验",
};

function categoryLabel(cat: string) {
  return CATEGORY_LABELS[cat] ?? cat;
}

function fmtBytes(b: number) {
  if (b < 1024) return `${b} B`;
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
  return `${(b / (1024 * 1024)).toFixed(1)} MB`;
}

function countByStatus(stats: KnowledgeStats | null, status: string): number {
  if (!stats?.by_status) return 0;
  return stats.by_status.find((s) => s.Status === status)?.Count ?? 0;
}

// ---- Doc card ----

function DocCard({ doc, selected, onClick }: { doc: KnowledgeDoc; selected: boolean; onClick: () => void }) {
  const sm = STATUS_LABELS[doc.status as KBStatus] ?? STATUS_LABELS.unverified;
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "w-full text-left px-3 py-2.5 border-b last:border-b-0 hover:bg-muted/60 transition-colors",
        selected && "bg-muted",
      )}
    >
      <div className="flex items-start gap-2">
        <span className={cn(
          "mt-1.5 inline-block w-2 h-2 rounded-full shrink-0",
          doc.status === "verified" && "bg-green-500",
          doc.status === "unverified" && "bg-yellow-500",
          doc.status === "negative" && "bg-red-500",
        )} />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium leading-snug line-clamp-2">{doc.title}</p>
          <p className="text-xs text-muted-foreground mt-0.5 truncate">{categoryLabel(doc.category)}</p>
          <div className="flex items-center gap-1.5 mt-1 flex-wrap">
            <Badge variant="outline" className={cn("text-[10px] px-1.5 py-0 h-4", sm.cls)}>
              {sm.label}
            </Badge>
            <span className="text-[10px] text-muted-foreground">复用 {doc.hit_count}</span>
            {doc.indexed && <span className="text-[10px] text-blue-500">语义</span>}
          </div>
        </div>
        <ChevronRightIcon className="shrink-0 w-3.5 h-3.5 text-muted-foreground mt-1" />
      </div>
    </button>
  );
}

// ---- Detail panel ----

function DocDetail({ doc, onStatusChange, onEdit, onDelete }: {
  doc: KnowledgeDoc; onStatusChange: (id: number, status: string) => void;
  onEdit: (doc: KnowledgeDoc) => void; onDelete: (id: number) => void;
}) {
  const sm = STATUS_LABELS[doc.status as KBStatus] ?? STATUS_LABELS.unverified;
  const nextStatuses = (["verified", "unverified", "negative"] as KBStatus[]).filter((s) => s !== doc.status);
  return (
    <div className="p-4 space-y-4">
      <div className="flex items-start justify-between gap-2">
        <h3 className="text-base font-semibold leading-snug">{doc.title}</h3>
        <Badge variant="outline" className={cn("shrink-0 text-xs", sm.cls)}>{sm.label}</Badge>
      </div>
      <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span>归类：<span className="text-foreground">{categoryLabel(doc.category)}</span></span>
        <span>来源：<span className="text-foreground">{doc.source}</span></span>
        <span>复用次数：<span className="text-foreground">{doc.hit_count}</span></span>
        <span>大小：<span className="text-foreground">{fmtBytes(doc.bytes)}</span></span>
        {doc.chunks > 0 && <span>向量块：<span className="text-foreground">{doc.chunks}</span></span>}
        {doc.file_name && <span>文件：<span className="text-foreground">{doc.file_name}</span></span>}
      </div>
      {doc.content && (
        <div className="rounded-md border bg-muted/40 p-3 text-xs font-mono whitespace-pre-wrap max-h-64 overflow-y-auto">
          {doc.content}
        </div>
      )}
      <div className="flex gap-2 flex-wrap">
        <Button size="sm" variant="outline" className="h-7 text-xs gap-1" onClick={() => onEdit(doc)}>
          <PencilIcon className="w-3.5 h-3.5" />编辑
        </Button>
        <Button size="sm" variant="outline" className="h-7 text-xs gap-1 text-red-500 border-red-500/30 hover:bg-red-500/10" onClick={() => onDelete(doc.id)}>
          <Trash2Icon className="w-3.5 h-3.5" />删除
        </Button>
        {nextStatuses.map((s) => (
          <Button key={s} size="sm" variant="outline" className="h-7 text-xs" onClick={() => onStatusChange(doc.id, s)}>
            {s === "verified" && <CheckCircle2Icon className="w-3.5 h-3.5 mr-1 text-green-500" />}
            {s === "negative" && <XCircleIcon className="w-3.5 h-3.5 mr-1 text-red-500" />}
            {s === "unverified" && <SparklesIcon className="w-3.5 h-3.5 mr-1 text-yellow-500" />}
            标记为{STATUS_LABELS[s].label}
          </Button>
        ))}
      </div>
    </div>
  );
}

// ---- Overview panel ----

function OverviewPanel({ stats }: { stats: KnowledgeStats | null }) {
  const total = (stats?.global_docs ?? 0) + (stats?.task_docs ?? 0);
  const verified = countByStatus(stats, "verified");
  const negative = countByStatus(stats, "negative");
  return (
    <div className="p-4 space-y-4">
      <div>
        <h2 className="text-base font-semibold">知识库概览</h2>
        <p className="text-xs text-muted-foreground mt-0.5">
          这是 agent 与你共用的跨靶标武器库：任务开局按目标自动预查，命中即注入 prompt，agent 取全文时自动记一次复用（hit_count）。本页只读，翻看不记复用。
        </p>
      </div>
      <div className="grid grid-cols-3 gap-2">
        {[
          { label: "条目总数", value: total },
          { label: "已验证",   value: verified },
          { label: "负面情报", value: negative },
        ].map((s) => (
          <Card key={s.label} className="p-3">
            <p className="text-2xl font-bold">{s.value}</p>
            <p className="text-xs text-muted-foreground mt-0.5">{s.label}</p>
          </Card>
        ))}
      </div>
      {stats?.semantic_ready && (
        <div className="rounded-md border border-blue-500/20 bg-blue-500/5 px-3 py-2 text-xs text-blue-600 flex items-center gap-1.5">
          <SparklesIcon className="w-3.5 h-3.5" />
          语义搜索已就绪（pgvector）
        </div>
      )}
      <Card>
        <CardHeader className="pb-2 pt-3 px-3">
          <CardTitle className="text-sm flex items-center gap-1.5">
            <SparklesIcon className="w-4 h-4 text-yellow-500" />
            三道钩子（agent 侧自动，无需人工）
          </CardTitle>
        </CardHeader>
        <CardContent className="px-3 pb-3 text-xs text-muted-foreground space-y-1">
          <p>① 开局强制查：worker 每个 intent 启动、planner 首轮拆解时自动检索并注入 prompt（弹药≤3 / 打法≤2 / 负面≤2）。</p>
          <p>② 结果分档回填：弹药（已验证可直接取用）/ 打法（判据参考）/ 负面（已否则再走）。</p>
          <p>③ 取用即记复用：agent 调 kb_get 取全文时 hit_count+1，写 used_on="ARTEX task#N"。</p>
        </CardContent>
      </Card>
      <Card>
        <CardHeader className="pb-2 pt-3 px-3">
          <CardTitle className="text-sm flex items-center gap-1.5">
            <ShieldOffIcon className="w-4 h-4 text-muted-foreground" />
            回填（写库）规则
          </CardTitle>
        </CardHeader>
        <CardContent className="px-3 pb-3 text-xs text-muted-foreground">
          漏洞在「发现」页被置为「已确认」，或复测 verdict=reproduced 时，系统会按三件套约束
          （category+other、engagement+asset_target、verified+verified_note）自动回填；
          缺项拒绝写入并记日志，不影响主流程。回填走 ON CONFLICT(code) 合并，不会覆盖已有复用统计。
        </CardContent>
      </Card>
    </div>
  );
}

// ---- Create dialog ----

function CreateDocDialog({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: () => void }) {
  const [form, setForm] = React.useState({ category: "auto-finding", title: "", content: "", status: "unverified" as KBStatus });
  const [loading, setLoading] = React.useState(false);

  async function handleSubmit() {
    if (!form.title.trim() || !form.content.trim()) { toast.error("标题和内容不能为空"); return; }
    setLoading(true);
    try {
      await api.createKnowledgeDoc({ ...form, scope: "global" });
      toast.success("条目已创建");
      onCreated();
      onClose();
      setForm({ category: "auto-finding", title: "", content: "", status: "unverified" });
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "创建失败");
    } finally {
      setLoading(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>新建条目</DialogTitle>
          <DialogDescription>手动写入一条知识到武器库</DialogDescription>
        </DialogHeader>
        <FieldGroup className="space-y-3">
          <Field>
            <FieldLabel>归类</FieldLabel>
            <Select value={form.category} onValueChange={(v) => setForm((f) => ({ ...f, category: v }))}>
              <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                {Object.entries(CATEGORY_LABELS).map(([k, v]) => <SelectItem key={k} value={k}>{v}</SelectItem>)}
                <SelectItem value="other">其他</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field>
            <FieldLabel>状态</FieldLabel>
            <Select value={form.status} onValueChange={(v) => setForm((f) => ({ ...f, status: v as KBStatus }))}>
              <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="verified">已验证（弹药）</SelectItem>
                <SelectItem value="unverified">未验证（待审查）</SelectItem>
                <SelectItem value="negative">负面（排除）</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field>
            <FieldLabel>标题</FieldLabel>
            <input
              className="w-full rounded-md border bg-background px-3 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              placeholder="简短描述，如：shiro 反序列化 rememberMe bypass"
              value={form.title}
              onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))}
            />
          </Field>
          <Field>
            <FieldLabel>内容</FieldLabel>
            <Textarea rows={6} placeholder="详细内容，支持 Markdown" value={form.content}
              onChange={(e) => setForm((f) => ({ ...f, content: e.target.value }))} />
          </Field>
        </FieldGroup>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={handleSubmit} disabled={loading}>
            {loading ? <Spinner className="mr-2" /> : null}创建
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---- Edit dialog ----

function EditDocDialog({ doc, onClose, onSaved }: { doc: KnowledgeDoc; onClose: () => void; onSaved: (d: KnowledgeDoc) => void }) {
  const [form, setForm] = React.useState({ category: doc.category, title: doc.title, content: doc.content ?? "" });
  const [loading, setLoading] = React.useState(false);
  async function handleSave() {
    if (!form.title.trim() || !form.content.trim()) { toast.error("标题和内容不能为空"); return; }
    setLoading(true);
    try {
      await api.updateKnowledgeDoc(doc.id, form);
      toast.success("已更新");
      onSaved({ ...doc, ...form });
      onClose();
    } catch (e: unknown) { toast.error(e instanceof Error ? e.message : "更新失败"); }
    finally { setLoading(false); }
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader><DialogTitle>编辑条目</DialogTitle></DialogHeader>
        <FieldGroup className="space-y-3">
          <Field>
            <FieldLabel>归类</FieldLabel>
            <Select value={form.category} onValueChange={(v) => setForm((f) => ({ ...f, category: v }))}>
              <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                {Object.entries(CATEGORY_LABELS).map(([k, v]) => <SelectItem key={k} value={k}>{v}</SelectItem>)}
                <SelectItem value="other">其他</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field>
            <FieldLabel>标题</FieldLabel>
            <input className="w-full rounded-md border bg-background px-3 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              value={form.title} onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))} />
          </Field>
          <Field>
            <FieldLabel>内容</FieldLabel>
            <Textarea rows={8} value={form.content} onChange={(e) => setForm((f) => ({ ...f, content: e.target.value }))} />
          </Field>
        </FieldGroup>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={handleSave} disabled={loading}>{loading ? <Spinner className="mr-2" /> : null}保存</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---- Config panel ----

function ConfigPanel() {
  const [cfg, setCfg] = React.useState<KnowledgeConfig | null>(null);
  const [form, setForm] = React.useState({ enabled: false, base_url: "", model: "", api_key: "", dim: 1024 });
  const [loading, setLoading] = React.useState(false);
  const [testing, setTesting] = React.useState(false);

  React.useEffect(() => {
    void api.getKnowledgeConfig().then((c) => { setCfg(c); setForm({ enabled: c.enabled, base_url: c.base_url, model: c.model, api_key: "", dim: c.dim }); });
  }, []);

  async function handleSave() {
    setLoading(true);
    try {
      const updated = await api.saveKnowledgeConfig(form);
      setCfg(updated);
      toast.success("配置已保存");
    } catch (e: unknown) { toast.error(e instanceof Error ? e.message : "保存失败"); }
    finally { setLoading(false); }
  }

  async function handleTest() {
    setTesting(true);
    try {
      const r = await api.testEmbedder();
      toast.success(`连通正常，向量维度 ${r.dim}`);
    } catch (e: unknown) { toast.error(e instanceof Error ? e.message : "连通失败"); }
    finally { setTesting(false); }
  }

  async function handleReindex() {
    if (!confirm("重建索引会清空所有向量块后重新计算，耗时较长。确认执行？")) return;
    setLoading(true);
    try {
      const r = await api.reindexKnowledge();
      toast.success(r.message);
    } catch (e: unknown) { toast.error(e instanceof Error ? e.message : "重建失败"); }
    finally { setLoading(false); }
  }

  return (
    <div className="p-4 space-y-5 max-w-lg">
      <div>
        <h2 className="text-base font-semibold">向量检索配置</h2>
        <p className="text-xs text-muted-foreground mt-0.5">兼容 OpenAI embeddings API，推荐使用智谱 embedding-3（1024维）。</p>
      </div>
      <FieldGroup className="space-y-3">
        <Field>
          <FieldLabel>
            <label className="flex items-center gap-2 cursor-pointer">
              <Checkbox checked={form.enabled} onCheckedChange={(v) => setForm((f) => ({ ...f, enabled: !!v }))} />
              启用语义搜索
            </label>
          </FieldLabel>
        </Field>
        <Field>
          <FieldLabel>Base URL</FieldLabel>
          <input className="w-full rounded-md border bg-background px-3 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            placeholder="https://open.bigmodel.cn/api/paas/v4" value={form.base_url}
            onChange={(e) => setForm((f) => ({ ...f, base_url: e.target.value }))} />
        </Field>
        <Field>
          <FieldLabel>模型</FieldLabel>
          <input className="w-full rounded-md border bg-background px-3 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            placeholder="embedding-3" value={form.model}
            onChange={(e) => setForm((f) => ({ ...f, model: e.target.value }))} />
        </Field>
        <Field>
          <FieldLabel>API Key {cfg?.has_key && <span className="text-xs text-green-500 ml-1">（已设置）</span>}</FieldLabel>
          <input type="password" className="w-full rounded-md border bg-background px-3 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            placeholder="留空保持现有 key" value={form.api_key}
            onChange={(e) => setForm((f) => ({ ...f, api_key: e.target.value }))} />
        </Field>
        <Field>
          <FieldLabel>向量维度</FieldLabel>
          <input type="number" className="w-full rounded-md border bg-background px-3 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            value={form.dim} onChange={(e) => setForm((f) => ({ ...f, dim: parseInt(e.target.value) || 1024 }))} />
        </Field>
      </FieldGroup>
      <div className="flex gap-2 flex-wrap">
        <Button onClick={handleSave} disabled={loading} className="h-7 text-xs">
          {loading ? <Spinner className="mr-1" /> : null}保存配置
        </Button>
        <Button variant="outline" onClick={handleTest} disabled={testing} className="h-7 text-xs">
          {testing ? <Spinner className="mr-1" /> : <SparklesIcon className="w-3.5 h-3.5 mr-1" />}测试连通
        </Button>
        <Button variant="outline" onClick={handleReindex} disabled={loading} className="h-7 text-xs text-yellow-600 border-yellow-600/30 hover:bg-yellow-500/10">
          重建向量索引
        </Button>
      </div>
    </div>
  );
}

// ---- Main page ----

export default function KnowledgePage() {
  const [docs, setDocs] = React.useState<KnowledgeDoc[]>([]);
  const [stats, setStats] = React.useState<KnowledgeStats | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [q, setQ] = React.useState("");
  const [filterStatus, setFilterStatus] = React.useState("all");
  const [filterCategory, setFilterCategory] = React.useState("all");
  const [onlyVerified, setOnlyVerified] = React.useState(false);
  const [selected, setSelected] = React.useState<KnowledgeDoc | null>(null);
  const [createOpen, setCreateOpen] = React.useState(false);
  const [editDoc, setEditDoc] = React.useState<KnowledgeDoc | null>(null);
  const [showConfig, setShowConfig] = React.useState(false);

  async function load() {
    setLoading(true);
    try {
      const [docsRes, statsRes] = await Promise.all([
        api.listKnowledgeDocs({
          category: filterCategory !== "all" ? filterCategory : undefined,
          status: onlyVerified ? "verified" : filterStatus !== "all" ? filterStatus : undefined,
        }),
        api.knowledgeStats(),
      ]);
      setDocs(docsRes.docs ?? []);
      setStats(statsRes);
    } catch { toast.error("加载失败"); }
    finally { setLoading(false); }
  }

  // eslint-disable-next-line react-hooks/exhaustive-deps
  React.useEffect(() => { void load(); }, [filterStatus, filterCategory, onlyVerified]);

  async function handleDelete(id: number) {
    if (!confirm("确认删除该条目？此操作不可撤销。")) return;
    try {
      await api.deleteKnowledgeDoc(id);
      toast.success("已删除");
      setSelected((prev) => prev?.id === id ? null : prev);
      setDocs((prev) => prev.filter((d) => d.id !== id));
      void api.knowledgeStats().then(setStats);
    } catch { toast.error("删除失败"); }
  }

  async function handleStatusChange(id: number, status: string) {
    try {
      await api.updateKnowledgeDocStatus(id, status);
      toast.success("状态已更新");
      setSelected((prev) => prev?.id === id ? { ...prev, status: status as KBStatus } : prev);
      setDocs((prev) => prev.map((d) => d.id === id ? { ...d, status: status as KBStatus } : d));
      void api.knowledgeStats().then(setStats);
    } catch { toast.error("更新失败"); }
  }

  const filtered = React.useMemo(() => {
    const qLower = q.toLowerCase();
    return docs.filter((d) =>
      !qLower || d.title.toLowerCase().includes(qLower) || d.category.toLowerCase().includes(qLower) || (d.content ?? "").toLowerCase().includes(qLower),
    );
  }, [docs, q]);

  const groups = React.useMemo(() => [
    { label: "弹药（已验证，可直接取用）", docs: filtered.filter((d) => d.status === "verified") },
    { label: "待审（未验证）",             docs: filtered.filter((d) => d.status === "unverified") },
    { label: "负面情报（排除）",           docs: filtered.filter((d) => d.status === "negative") },
  ].filter((g) => g.docs.length > 0), [filtered]);

  const categories = React.useMemo(() => Array.from(new Set(docs.map((d) => d.category).filter(Boolean))).sort(), [docs]);

  return (
    <div className="flex h-full overflow-hidden">
      {/* Left panel */}
      <div className="w-80 shrink-0 border-r flex flex-col overflow-hidden">
        <div className="p-2 border-b space-y-2">
          <InputGroup>
            <InputGroupAddon><SearchIcon className="w-3.5 h-3.5 text-muted-foreground" /></InputGroupAddon>
            <InputGroupInput placeholder="搜索标题 / 归类 / 内容" value={q} onChange={(e) => setQ(e.target.value)} />
          </InputGroup>
          <div className="flex gap-1.5">
            <Select value={filterStatus} onValueChange={setFilterStatus}>
              <SelectTrigger className="h-7 text-xs flex-1"><SelectValue placeholder="全部状态" /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部状态</SelectItem>
                <SelectItem value="verified">已验证</SelectItem>
                <SelectItem value="unverified">未验证</SelectItem>
                <SelectItem value="negative">负面</SelectItem>
              </SelectContent>
            </Select>
            <Select value={filterCategory} onValueChange={setFilterCategory}>
              <SelectTrigger className="h-7 text-xs flex-1"><SelectValue placeholder="全部归类" /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部归类</SelectItem>
                {categories.map((c) => <SelectItem key={c} value={c}>{categoryLabel(c)}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
          <div className="flex items-center justify-between">
            <label className="flex items-center gap-1.5 text-xs cursor-pointer select-none">
              <Checkbox checked={onlyVerified} onCheckedChange={(v) => setOnlyVerified(!!v)} className="w-3.5 h-3.5" />
              只看已验证
            </label>
            <button type="button" className="text-xs text-muted-foreground hover:text-foreground flex items-center gap-1"
              onClick={() => { setQ(""); setFilterStatus("all"); setFilterCategory("all"); setOnlyVerified(false); }}>
              <FilterXIcon className="w-3 h-3" />重置
            </button>
          </div>
          <p className="text-xs text-muted-foreground">命中 {filtered.length} 条</p>
        </div>
        <div className="flex-1 overflow-y-auto">
          {loading && <div className="flex items-center justify-center py-8"><Spinner /></div>}
          {!loading && filtered.length === 0 && (
            <div className="flex flex-col items-center justify-center py-12 text-muted-foreground gap-2">
              <BookMarkedIcon className="w-8 h-8" /><p className="text-sm">暂无条目</p>
            </div>
          )}
          {!loading && groups.map((g) => (
            <div key={g.label}>
              <div className="px-3 py-1.5 bg-muted/40 border-b text-xs font-medium text-muted-foreground sticky top-0">
                {g.label} <span className="ml-1">{g.docs.length}</span>
              </div>
              {g.docs.map((doc) => (
                <DocCard key={doc.id} doc={doc} selected={selected?.id === doc.id} onClick={() => setSelected(doc)} />
              ))}
            </div>
          ))}
        </div>
      </div>

      {/* Right panel */}
      <div className="flex-1 overflow-y-auto">
        <div className="flex items-center justify-between px-4 py-3 border-b">
          <div>
            <h1 className="text-base font-semibold">武器库（知识库）</h1>
            <p className="text-xs text-muted-foreground">跨靶标武器库 · agent 开局自动预查 / 命中即用</p>
          </div>
          <div className="flex items-center gap-2">
            <Button size="sm" variant="outline" className="h-7 text-xs gap-1" onClick={() => void load()}>
              <RefreshCwIcon className="w-3.5 h-3.5" />刷新
            </Button>
            <Button size="sm" variant="outline" className="h-7 text-xs gap-1" onClick={() => { setShowConfig(!showConfig); setSelected(null); }}>
              <SettingsIcon className="w-3.5 h-3.5" />配置
            </Button>
            <Button size="sm" className="h-7 text-xs gap-1" onClick={() => setCreateOpen(true)}>
              <PlusIcon className="w-3.5 h-3.5" />新建条目
            </Button>
          </div>
        </div>
        {showConfig ? (
          <ConfigPanel />
        ) : selected ? (
          <DocDetail doc={selected} onStatusChange={handleStatusChange} onEdit={setEditDoc} onDelete={handleDelete} />
        ) : (
          <OverviewPanel stats={stats} />
        )}
      </div>

      <CreateDocDialog open={createOpen} onClose={() => setCreateOpen(false)} onCreated={() => void load()} />
      {editDoc && (
        <EditDocDialog
          doc={editDoc}
          onClose={() => setEditDoc(null)}
          onSaved={(updated) => {
            setDocs((prev) => prev.map((d) => d.id === updated.id ? { ...d, ...updated } : d));
            setSelected((prev) => prev?.id === updated.id ? { ...prev, ...updated } : prev);
            setEditDoc(null);
          }}
        />
      )}
    </div>
  );
}
