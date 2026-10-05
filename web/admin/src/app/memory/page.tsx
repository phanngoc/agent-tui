"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { PlusIcon, SearchIcon, PinIcon, XIcon, LayersIcon, FlameIcon, Trash2Icon, PencilIcon, SparklesIcon } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useGateway, useVersion } from "@/lib/store";
import type { Activity, Install, LearnStatus, LogEntry, MemoryRecord, MemoryStats, Scene, Scope } from "@/lib/types";
import { Ago, Empty, ErrorNote, Field, Mono, NativeSelect, PageHeader, Pre, ScopeBadge } from "@/components/common";
import { ActivityItem } from "@/components/learn-feed";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { memoryTypeHint, memoryTypeLabel, stamp } from "@/lib/format";
import { ConsolidateDialog, type ConsolidateReport } from "@/components/consolidate-dialog";
import { cn } from "@/lib/utils";

export default function MemoryPage() {
  return (
    <React.Suspense fallback={null}>
      <Memory />
    </React.Suspense>
  );
}

function Memory() {
  const params = useSearchParams();
  const router = useRouter();
  const tab = params.get("tab") ?? "records";
  const root = useGateway((s) => s.root);
  const [consolidating, setConsolidating] = React.useState(false);
  const [report, setReport] = React.useState<ConsolidateReport[] | null>(null);
  return (
    <div>
      <PageHeader
        title="Memory"
        description={
          <>
            What the agent learned, in TencentDB&apos;s layers: <b>records</b> (L1 atoms) → <b>scenes</b> (L2) → <b>persona</b> (L3). Global memory follows you;
            project memory stays with its project.{" "}
            {root ? (
              <>
                Showing global and <Mono>{root}</Mono>.
              </>
            ) : (
              <>Showing every project&apos;s — pick one at the top to focus on it.</>
            )}
          </>
        }
        actions={
          <Button
            variant="outline"
            size="sm"
            disabled={consolidating}
            title="Fold every memory into scenes and rewrite the persona and doctrine now, without waiting for the schedule"
            onClick={async () => {
              setConsolidating(true);
              const t = toast.loading(root ? "Consolidating this project's memory…" : "Consolidating every store…");
              try {
                const r = await api.post<{ reports: ConsolidateReport[] }>("/api/memory/consolidate", { root, all: !root });
                setReport(r.reports);
                toast.dismiss(t);
              } catch (e) {
                toast.error((e as Error).message, { id: t });
              } finally {
                setConsolidating(false);
              }
            }}
          >
            <LayersIcon /> {consolidating ? "Consolidating…" : root ? "Consolidate this project" : "Consolidate all"}
          </Button>
        }
      />
      <ConsolidateDialog report={report} onClose={() => setReport(null)} />
      <Tabs value={tab} onValueChange={(v) => router.replace(`/memory?tab=${v}`)} className="p-6">
        <TabsList>
          <TabsTrigger value="records">Records</TabsTrigger>
          <TabsTrigger value="scenes">Scenes &amp; persona</TabsTrigger>
          <TabsTrigger value="learning">Learning activity</TabsTrigger>
          <TabsTrigger value="import">Import</TabsTrigger>
        </TabsList>
        <TabsContent value="records" className="pt-4">
          <Records />
        </TabsContent>
        <TabsContent value="scenes" className="pt-4">
          <Scenes />
        </TabsContent>
        <TabsContent value="learning" className="pt-4">
          <Learning />
        </TabsContent>
        <TabsContent value="import" className="pt-4">
          <ImportMemory />
        </TabsContent>
      </Tabs>
    </div>
  );
}

const blank = (scope: Scope): MemoryRecord => ({
  id: "",
  content: "",
  type: scope === "global" ? "instruction" : "work_fact",
  priority: 80,
  scope,
  created: "",
  updated: "",
  version: 0,
});

interface MemHit {
  record: MemoryRecord;
  score: number;
  project?: string;
  project_name: string;
  dir: string;
}

interface StoreStats extends MemoryStats {
  project?: string;
  project_name: string;
}

function Records() {
  const params = useSearchParams();
  const router = useRouter();
  const root = useGateway((s) => s.root);
  const ids = (params.get("ids") ?? "").split(",").filter(Boolean);
  const [scope, setScope] = React.useState("");
  const [type, setType] = React.useState("");
  const [q, setQ] = React.useState("");
  const [store, setStore] = React.useState("");
  const [open, setOpen] = React.useState<MemHit | null>(null);
  const [dismissed, setDismissed] = React.useState("");
  const v = useVersion("memory");
  const { data, error } = useFetch<{ hits: MemHit[]; stats: StoreStats[]; types: string[]; all: boolean }>(
    // A link to particular records looks in every store: it may come from
    // any project's learning.
    "/api/memory" + qs({ root, scope, type, q, all: ids.length ? 1 : undefined }),
    [v],
  );
  let hits = data?.hits ?? [];
  if (ids.length) hits = hits.filter((h) => ids.includes(h.record.id));
  if (store) hits = hits.filter((h) => h.dir === store);
  const many = (data?.stats.length ?? 0) > 2 || !!data?.all;

  // A single id in the URL opens it, until it is closed.
  const linked = ids.length === 1 && dismissed !== ids[0] ? (data?.hits.find((x) => x.record.id === ids[0]) ?? null) : null;
  const current = open ?? linked;

  return (
    <div className="space-y-4">
      <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
        {(data?.stats ?? []).map((s) => (
          <button
            key={s.dir}
            onClick={() => setStore(store === s.dir ? "" : s.dir)}
            title={s.dir + "\nClick to show only this store"}
            className={cn(
              "rounded-xl border p-3 text-left transition-colors hover:bg-muted/40",
              store === s.dir && "border-primary bg-muted/60",
              s.records === 0 && "opacity-70",
            )}
          >
            <div className="flex items-center gap-2">
              <ScopeBadge scope={s.scope} />
              <span className="min-w-0 truncate text-sm font-medium">{s.scope === "global" ? "global" : s.project_name}</span>
              <span className="ml-auto text-xs text-muted-foreground">
                <Ago at={s.updated} />
              </span>
            </div>
            <div className="mt-1 text-xs text-muted-foreground">
              <b className="text-foreground">{s.records}</b> records · {s.scenes} scenes · {s.scope === "global" ? "persona" : "doctrine"}{" "}
              {s.persona ? "written" : "not yet"}
            </div>
            {s.records > 0 && (
              <div className="mt-2 flex flex-wrap gap-1">
                {Object.entries(s.by_type).map(([t, n]) => (
                  <Badge key={t} variant="outline" className="font-normal">
                    {memoryTypeLabel[t] ?? t} {n}
                  </Badge>
                ))}
              </div>
            )}
          </button>
        ))}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative w-80">
          <SearchIcon className="absolute top-2 left-2 size-4 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search as recall would (BM25)" className="pl-8" />
        </div>
        <NativeSelect value={scope} onChange={setScope} placeholder="all scopes" options={[{ value: "global", label: "global" }, { value: "project", label: "project" }]} />
        <NativeSelect value={type} onChange={setType} placeholder="all types" options={(data?.types ?? []).map((t) => ({ value: t, label: memoryTypeLabel[t] ?? t }))} />
        {store && (
          <Badge variant="secondary" className="gap-1">
            {data?.stats.find((s) => s.dir === store)?.project_name ?? "store"} only
            <button onClick={() => setStore("")}>
              <XIcon className="size-3" />
            </button>
          </Badge>
        )}
        {ids.length > 0 && (
          <Badge variant="secondary" className="gap-1">
            {ids.length} selected
            <button onClick={() => router.replace("/memory")}>
              <XIcon className="size-3" />
            </button>
          </Badge>
        )}
        <span className="text-xs text-muted-foreground">{hits.length} shown</span>
        <Button size="sm" className="ml-auto" onClick={() => setOpen({ record: blank(root ? "project" : "global"), score: 0, project: root, project_name: "", dir: "" })}>
          <PlusIcon /> New memory
        </Button>
      </div>

      <ErrorNote error={error} />
      {hits.length === 0 ? (
        <Empty title="No memories here">
          {data && data.stats.every((s) => s.records === 0) ? "Memories appear as the agent learns from conversations, or add one by hand." : "Nothing matches these filters."}
        </Empty>
      ) : (
        <div className="rounded-xl border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-[42%]">Memory</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>{many ? "Store" : "Scope"}</TableHead>
                <TableHead className="text-right">Priority</TableHead>
                <TableHead>Origin</TableHead>
                <TableHead className="text-right">Recalled</TableHead>
                <TableHead>Updated</TableHead>
                {q && <TableHead className="text-right">Score</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {hits.map((h) => {
                const r = h.record;
                return (
                  <TableRow key={h.dir + r.id} className="cursor-pointer" onClick={() => setOpen(h)}>
                    <TableCell className="max-w-0 whitespace-normal">
                      <div className="line-clamp-2 text-sm">
                        {r.pinned && <PinIcon className="mr-1 inline size-3 text-primary" />}
                        {r.content}
                      </div>
                      {r.scene && <div className="truncate text-xs text-muted-foreground">{r.scene}</div>}
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary" className="font-normal" title={memoryTypeHint[r.type]}>
                        {memoryTypeLabel[r.type] ?? r.type}
                      </Badge>
                    </TableCell>
                    <TableCell className="max-w-40">
                      {many && r.scope === "project" ? (
                        <span className="block truncate text-xs" title={h.project || h.dir}>
                          {h.project_name}
                        </span>
                      ) : (
                        <ScopeBadge scope={r.scope} />
                      )}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{r.priority < 0 ? "always" : r.priority}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {r.origin}
                      {r.version > 1 && ` · v${r.version}`}
                    </TableCell>
                    <TableCell className="text-right tabular-nums text-xs">{r.hits ?? 0}</TableCell>
                    <TableCell className="text-xs">
                      <Ago at={r.updated} />
                    </TableCell>
                    {q && <TableCell className="text-right tabular-nums text-xs">{h.score.toFixed(2)}</TableCell>}
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
      <RecordSheet
        key={current ? current.dir + current.record.id + current.record.version : "none"}
        hit={current}
        root={root}
        types={data?.types ?? []}
        onClose={() => {
          setOpen(null);
          setDismissed(ids[0] ?? "");
        }}
      />
    </div>
  );
}

function RecordSheet({ hit, root, types, onClose }: { hit: MemHit | null; root: string; types: string[]; onClose: () => void }) {
  const record = hit?.record ?? null;
  const [r, setR] = React.useState<MemoryRecord | null>(record);
  const [err, setErr] = React.useState<string | null>(null);
  // The record's own project, which is not always the one picked at the top.
  const projectRoot = hit?.project || root;
  const { data: log } = useFetch<LogEntry[]>(record?.id ? "/api/memory/log" + qs({ root: projectRoot, dir: hit?.dir || undefined, id: record.id }) : null, [record?.id]);
  if (!r || !hit) return null;
  const isNew = !r.id;
  const sameStore = !isNew && r.scope === record?.scope && !!hit.dir;
  const save = async () => {
    try {
      await api.put("/api/memory" + qs({ dir: sameStore ? hit.dir : undefined }), { root: projectRoot, record: r, from: record?.scope });
      toast.success(isNew ? "Memory added" : "Memory saved");
      onClose();
    } catch (e) {
      setErr((e as Error).message);
    }
  };
  const remove = async () => {
    if (!confirm("Delete this memory? The write log keeps a copy.")) return;
    try {
      await api.del("/api/memory" + qs({ root: projectRoot, dir: hit.dir || undefined, scope: r.scope, id: r.id }));
      toast.success("Memory deleted");
      onClose();
    } catch (e) {
      setErr((e as Error).message);
    }
  };
  return (
    <Sheet open={!!record} onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="w-full overflow-auto sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>{isNew ? "New memory" : "Memory"}</SheetTitle>
          <SheetDescription>
            {isNew ? "Written by hand; origin “manual”." : <Mono>{r.id}</Mono>}
            {!isNew && r.scope === "project" && <> · {hit.project || hit.project_name}</>}
          </SheetDescription>
        </SheetHeader>
        <div className="space-y-4 px-4 pb-6">
          <Field label="Content" hint="Self-contained: it is read later with no conversation around it.">
            <Textarea rows={5} value={r.content} onChange={(e) => setR({ ...r, content: e.target.value })} />
          </Field>
          <div className="grid grid-cols-3 gap-3">
            <Field label="Type" hint={memoryTypeHint[r.type]}>
              <NativeSelect value={r.type} onChange={(t) => setR({ ...r, type: t })} options={types.map((t) => ({ value: t, label: memoryTypeLabel[t] ?? t }))} />
            </Field>
            <Field label="Scope">
              <NativeSelect
                value={r.scope}
                onChange={(s) => setR({ ...r, scope: s as Scope })}
                options={[{ value: "global", label: "global" }, ...(projectRoot ? [{ value: "project", label: "project" }] : [])]}
              />
            </Field>
            <Field label="Priority" hint="-1 = absolute rule">
              <Input type="number" min={-1} max={100} value={r.priority} onChange={(e) => setR({ ...r, priority: Number(e.target.value) })} />
            </Field>
          </div>
          <div className="flex items-center gap-2">
            <Switch checked={!!r.pinned} onCheckedChange={(c) => setR({ ...r, pinned: c })} />
            <span className="text-sm">Pinned — in every prompt, not only when recalled</span>
          </div>
          <Field label="Scene">
            <Input value={r.scene ?? ""} onChange={(e) => setR({ ...r, scene: e.target.value })} />
          </Field>
          <ErrorNote error={err} />
          <div className="flex gap-2">
            <Button onClick={save}>Save</Button>
            {!isNew && (
              <Button variant="destructive" onClick={remove}>
                <Trash2Icon /> Delete
              </Button>
            )}
          </div>

          {!isNew && (
            <>
              <div className="space-y-1 rounded-xl border p-3 text-sm">
                <div className="mb-1 font-medium">Provenance</div>
                <Row k="origin">{r.origin || "—"}</Row>
                <Row k="session">
                  {r.session ? (
                    <Link className="text-primary hover:underline" href={`/sessions?id=${r.session}`}>
                      {r.session}
                      {r.sources?.length ? ` · messages #${r.sources.join(", #")}` : ""}
                    </Link>
                  ) : (
                    "—"
                  )}
                </Row>
                <Row k="created">{stamp(r.created)}</Row>
                <Row k="updated">
                  {stamp(r.updated)} (version {r.version})
                </Row>
                <Row k="recalled">
                  {r.hits ?? 0} times{r.last_hit ? `, last ${stamp(r.last_hit)}` : ""}
                </Row>
                <Row k="observed">{(r.timestamps ?? []).map((t) => new Date(t).toLocaleDateString()).join(", ") || "—"}</Row>
                {r.metadata && Object.keys(r.metadata).length > 0 && <Pre max="max-h-40">{JSON.stringify(r.metadata, null, 2)}</Pre>}
              </div>
              <div>
                <div className="mb-2 text-sm font-medium">History</div>
                <div className="space-y-2">
                  {(log ?? []).length === 0 && <div className="text-xs text-muted-foreground">no log entries</div>}
                  {(log ?? []).map((e, i) => (
                    <div key={i} className="rounded-lg border p-2 text-xs">
                      <div className="mb-1 flex items-center gap-2">
                        <Badge variant={e.op === "delete" ? "destructive" : e.op === "store" ? "secondary" : "default"} className="font-normal">
                          {e.op}
                        </Badge>
                        <span className="text-muted-foreground">{stamp(e.at)}</span>
                        <span className="text-muted-foreground">v{e.record.version}</span>
                        {e.record.id !== r.id && <span className="text-muted-foreground">absorbed into {e.record.id}</span>}
                      </div>
                      <div className="whitespace-pre-wrap">{e.record.content}</div>
                      {e.targets && e.targets.length > 0 && <div className="mt-1 text-muted-foreground">replaced: {e.targets.join(", ")}</div>}
                    </div>
                  ))}
                </div>
              </div>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function Row({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[6rem_1fr] gap-2">
      <span className="text-muted-foreground">{k}</span>
      <span className="min-w-0 break-words">{children}</span>
    </div>
  );
}

interface ScopeScenes {
  scope: Scope;
  persona: string;
  scenes: Scene[];
  dir: string;
  project?: string;
  project_name: string;
  records: number;
}

function Scenes() {
  const root = useGateway((s) => s.root);
  const v = useVersion("memory");
  const { data } = useFetch<ScopeScenes[]>("/api/memory/scenes" + qs({ root }), [v]);
  return (
    <div className="grid gap-6 xl:grid-cols-2">
      {(data ?? []).map((s) => (
        <div key={s.dir} className="space-y-3">
          <div className="flex items-center gap-2">
            <ScopeBadge scope={s.scope} />
            <span className="min-w-0 truncate text-sm font-medium" title={s.project || s.dir}>
              {s.scope === "global" ? "About you, everywhere" : s.project_name}
            </span>
            <span className="ml-auto shrink-0 text-xs text-muted-foreground">{s.records} records</span>
          </div>
          <PersonaCard key={s.persona} scope={s.scope} text={s.persona} root={s.project || root} dir={s.dir} />
          <SceneList scope={s.scope} scenes={s.scenes} root={s.project || root} dir={s.dir} />
        </div>
      ))}
    </div>
  );
}

function PersonaCard({ scope, text, root, dir }: { scope: Scope; text: string; root: string; dir: string }) {
  const [edit, setEdit] = React.useState(false);
  const [val, setVal] = React.useState(text);
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm">
          <SparklesIcon className="size-4" /> {scope === "global" ? "L3 · User persona" : "L3 · Project doctrine"}
          <Button size="icon-sm" variant="ghost" className="ml-auto" onClick={() => setEdit(!edit)}>
            <PencilIcon />
          </Button>
        </CardTitle>
        <CardDescription>Rewritten by the learner from the scenes; in every prompt. Edit it and the next rewrite starts from your version.</CardDescription>
      </CardHeader>
      <CardContent>
        {edit ? (
          <div className="space-y-2">
            <Textarea rows={12} value={val} onChange={(e) => setVal(e.target.value)} className="font-mono text-xs" />
            <Button
              size="sm"
              onClick={async () => {
                try {
                  await api.put("/api/memory/persona" + qs({ dir }), { root, scope, text: val });
                  setEdit(false);
                  toast.success("Saved");
                } catch (e) {
                  toast.error((e as Error).message);
                }
              }}
            >
              Save
            </Button>
          </div>
        ) : text ? (
          <Pre max="max-h-96" className="bg-transparent">
            {text}
          </Pre>
        ) : (
          <div className="text-sm text-muted-foreground">Not written yet: it appears once there are scenes to distil.</div>
        )}
      </CardContent>
    </Card>
  );
}

function SceneList({ scope, scenes, root, dir }: { scope: Scope; scenes: Scene[]; root: string; dir: string }) {
  const [editing, setEditing] = React.useState<(Scene & { old?: string }) | null>(null);
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">L2 · Scenes ({scenes.length}/15)</div>
        <Button size="xs" variant="ghost" className="ml-auto" onClick={() => setEditing({ file: "", summary: "", heat: 1, created: "", updated: "", body: "" })}>
          <PlusIcon /> scene
        </Button>
      </div>
      {scenes.length === 0 && <Empty title="No scenes yet">Scenes consolidate records after the first learning runs.</Empty>}
      {scenes.map((s) => (
        <details key={s.file} className="group rounded-xl border">
          <summary className="flex cursor-pointer list-none items-center gap-2 p-3">
            <FlameIcon className={cn("size-4", s.heat > 3 ? "text-orange-500" : "text-muted-foreground")} />
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium">{s.summary || s.file}</div>
              <div className="text-xs text-muted-foreground">
                {s.file} · heat {s.heat} · updated <Ago at={s.updated} />
              </div>
            </div>
            <Button
              size="icon-xs"
              variant="ghost"
              onClick={(e) => {
                e.preventDefault();
                setEditing({ ...s, old: s.file });
              }}
            >
              <PencilIcon />
            </Button>
            <Button
              size="icon-xs"
              variant="ghost"
              onClick={async (e) => {
                e.preventDefault();
                if (!confirm(`Delete scene ${s.file}?`)) return;
                await api.del("/api/memory/scenes" + qs({ root, dir, scope, file: s.file })).catch((er) => toast.error(er.message));
              }}
            >
              <Trash2Icon />
            </Button>
          </summary>
          <div className="border-t p-3">
            <Pre max="max-h-96" className="bg-transparent">
              {s.body}
            </Pre>
          </div>
        </details>
      ))}
      {editing && (
        <div className="space-y-2 rounded-xl border p-3">
          <Field label="File">
            <Input value={editing.file} onChange={(e) => setEditing({ ...editing, file: e.target.value })} placeholder="release-process.md" />
          </Field>
          <Field label="Summary">
            <Input value={editing.summary} onChange={(e) => setEditing({ ...editing, summary: e.target.value })} />
          </Field>
          <Field label="Body (Markdown)">
            <Textarea rows={10} className="font-mono text-xs" value={editing.body} onChange={(e) => setEditing({ ...editing, body: e.target.value })} />
          </Field>
          <div className="flex gap-2">
            <Button
              size="sm"
              onClick={async () => {
                try {
                  await api.put("/api/memory/scenes" + qs({ dir }), { root, scope, old_file: editing.old, scene: editing });
                  setEditing(null);
                } catch (e) {
                  toast.error((e as Error).message);
                }
              }}
            >
              Save scene
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setEditing(null)}>
              Cancel
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

function Learning() {
  const root = useGateway((s) => s.root);
  const v = useVersion("learn");
  const [stage, setStage] = React.useState("");
  const { data } = useFetch<{ status: LearnStatus; activity: Activity[]; enabled: boolean; knobs: Record<string, number> }>("/api/learn" + qs({ root }), [v]);
  const acts = (data?.activity ?? []).filter((a) => !stage || a.stage === stage);
  const st = data?.status;
  return (
    <div className="grid gap-4 lg:grid-cols-[22rem_1fr]">
      <div className="space-y-4">
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Learner</CardTitle>
            <CardDescription>{data?.enabled === false ? "Off for this project (Settings)." : "On."}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-1 text-sm">
            <Row k="model">{st?.model || "—"}</Row>
            {st?.error && <ErrorNote error={st.error} />}
            <Row k="working">{st?.busy || "idle"}</Row>
            <Row k="queue">{st?.queue ?? 0}</Row>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">How it decides</CardTitle>
          </CardHeader>
          <CardContent className="space-y-1.5 text-xs text-muted-foreground">
            <p>
              <b className="text-foreground">L1 extract</b> after {data?.knobs.every_n_turns} turns (warming up 1 → 2 → 4 → {data?.knobs.every_n_turns}), or after{" "}
              {data?.knobs.idle_minutes} idle minutes. New memories are judged against the 5 nearest: store, skip, update or merge.
            </p>
            <p>
              <b className="text-foreground">L2 scenes</b> fold new records in, at most every {data?.knobs.scene_interval_minutes} minutes per store, 15 scenes max.
            </p>
            <p>
              <b className="text-foreground">L3 persona</b> is rewritten when the scenes ask, when there is none, or after 20 new memories.
            </p>
            <p>
              <b className="text-foreground">Skills</b> are reviewed once a session has made {data?.knobs.skill_tool_calls} tool calls.
            </p>
            <p>
              <b className="text-foreground">Recall</b> takes the top {data?.knobs.recall_limit} by BM25 (≥ {data?.knobs.recall_threshold} once there are more), per prompt.
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Stores</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-xs">
            {Object.entries(st?.stores ?? {}).map(([dir, s]) => (
              <div key={dir} className="rounded-lg border p-2">
                <div className="truncate font-mono text-[11px]" title={dir}>
                  {dir}
                </div>
                <div className="text-muted-foreground">
                  {s.pending?.length ?? 0} waiting for scenes · {s.since_persona} since persona · scenes <Ago at={s.last_scenes} />
                </div>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>
      <div className="space-y-3">
        <div className="flex flex-wrap gap-1">
          {["", "extract", "scenes", "persona", "skill", "error", "note"].map((s) => (
            <button key={s} onClick={() => setStage(s)}>
              <Badge variant={stage === s ? "default" : "outline"} className="font-normal">
                {s || "all"}
              </Badge>
            </button>
          ))}
        </div>
        <div className="rounded-xl border">
          {acts.length === 0 && <Empty title="No activity" className="border-0" />}
          {acts.map((a, i) => (
            <ActivityItem key={i} a={a} />
          ))}
        </div>
      </div>
    </div>
  );
}

function ImportMemory() {
  const root = useGateway((s) => s.root);
  const { data, loading, error } = useFetch<Install[]>("/api/claude/installs", []);
  const [picked, setPicked] = React.useState<Record<string, boolean>>({});
  const [scope, setScope] = React.useState<Scope>("global");
  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Bring Claude Code&apos;s memory notes across — from this machine and from every WSL distribution that has one. They become records here (user → persona,
        feedback → instruction, reference → artifact, the rest → fact); nothing is written back.
      </p>
      {loading && !data && <div className="text-sm text-muted-foreground">Looking for Claude Code installs (WSL included)…</div>}
      <ErrorNote error={error} />
      {(data ?? []).map((inst) => (
        <Card key={inst.id}>
          <CardHeader>
            <CardTitle className="text-sm">
              {inst.label} <span className="font-mono text-xs font-normal text-muted-foreground">{inst.home}</span>
            </CardTitle>
            <CardDescription>{inst.memory.length} memory notes</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {inst.memory.map((m) => {
              const key = `${m.project}/${m.file}`;
              return (
                <label key={key} className="flex cursor-pointer gap-2 rounded-lg border p-2 text-sm hover:bg-muted/40">
                  <input type="checkbox" checked={!!picked[inst.id + "|" + key]} onChange={(e) => setPicked({ ...picked, [inst.id + "|" + key]: e.target.checked })} />
                  <div className="min-w-0">
                    <div className="font-medium">
                      {m.name || m.file} <span className="font-normal text-muted-foreground">· {m.type || "?"}</span>
                    </div>
                    <div className="text-xs text-muted-foreground">{m.description}</div>
                    <div className="truncate font-mono text-[11px] text-muted-foreground">{m.project}</div>
                  </div>
                </label>
              );
            })}
            {inst.memory.length > 0 && (
              <div className="flex items-center gap-2 pt-2">
                <NativeSelect
                  value={scope}
                  onChange={(s) => setScope(s as Scope)}
                  options={[{ value: "global", label: "into global memory" }, ...(root ? [{ value: "project", label: "into this project" }] : [])]}
                />
                <Button
                  size="sm"
                  onClick={async () => {
                    const files = Object.entries(picked)
                      .filter(([k, on]) => on && k.startsWith(inst.id + "|"))
                      .map(([k]) => k.slice(inst.id.length + 1));
                    if (!files.length) return toast.error("Pick some notes first");
                    try {
                      const r = await api.post<{ imported: number }>("/api/memory/import", { install: inst.id, files, scope, root });
                      toast.success(`Imported ${r.imported}`);
                    } catch (e) {
                      toast.error((e as Error).message);
                    }
                  }}
                >
                  Import selected
                </Button>
              </div>
            )}
          </CardContent>
        </Card>
      ))}
    </div>
  );
}
