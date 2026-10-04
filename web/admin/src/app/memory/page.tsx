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
  return (
    <div>
      <PageHeader
        title="Memory"
        description={
          <>
            What the agent learned, in TencentDB&apos;s layers: <b>records</b> (L1 atoms) → <b>scenes</b> (L2) → <b>persona</b> (L3). Global memory follows you;
            project memory stays with {root ? <Mono>{root}</Mono> : "a project (pick one above)"}.
          </>
        }
        actions={
          <Button
            variant="outline"
            size="sm"
            disabled={consolidating}
            onClick={async () => {
              setConsolidating(true);
              try {
                await api.post("/api/memory/consolidate", { root });
                toast.success("Scenes and persona rebuilt");
              } catch (e) {
                toast.error((e as Error).message);
              } finally {
                setConsolidating(false);
              }
            }}
          >
            <LayersIcon /> {consolidating ? "Consolidating…" : "Consolidate now"}
          </Button>
        }
      />
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

function Records() {
  const params = useSearchParams();
  const router = useRouter();
  const root = useGateway((s) => s.root);
  const ids = (params.get("ids") ?? "").split(",").filter(Boolean);
  const [scope, setScope] = React.useState("");
  const [type, setType] = React.useState("");
  const [q, setQ] = React.useState("");
  const [open, setOpen] = React.useState<MemoryRecord | null>(null);
  const [dismissed, setDismissed] = React.useState("");
  const v = useVersion("memory");
  const { data, error } = useFetch<{ hits: { record: MemoryRecord; score: number }[]; stats: MemoryStats[]; types: string[] }>(
    "/api/memory" + qs({ root, scope, type, q }),
    [v],
  );
  let hits = data?.hits ?? [];
  if (ids.length) hits = hits.filter((h) => ids.includes(h.record.id));

  // A single id in the URL opens it, until it is closed.
  const linked = ids.length === 1 && dismissed !== ids[0] ? (data?.hits.find((x) => x.record.id === ids[0])?.record ?? null) : null;
  const current = open ?? linked;

  return (
    <div className="space-y-4">
      <div className="grid gap-3 md:grid-cols-2">
        {(data?.stats ?? []).map((s) => (
          <div key={s.scope} className="rounded-xl border p-3">
            <div className="flex items-center gap-2">
              <ScopeBadge scope={s.scope} />
              <span className="text-sm font-medium">{s.records} records</span>
              <span className="text-xs text-muted-foreground">
                · {s.scenes} scenes · persona {s.persona ? "written" : "not yet"}
              </span>
              <span className="ml-auto text-xs text-muted-foreground">
                updated <Ago at={s.updated} />
              </span>
            </div>
            <div className="mt-2 flex flex-wrap gap-1">
              {Object.entries(s.by_type).map(([t, n]) => (
                <button key={t} onClick={() => setType(type === t ? "" : t)}>
                  <Badge variant={type === t ? "default" : "outline"} className="font-normal">
                    {memoryTypeLabel[t] ?? t} {n}
                  </Badge>
                </button>
              ))}
            </div>
            <div className="mt-2 truncate font-mono text-[11px] text-muted-foreground" title={s.dir}>
              {s.dir}
            </div>
          </div>
        ))}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative w-80">
          <SearchIcon className="absolute top-2 left-2 size-4 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search as recall would (BM25)" className="pl-8" />
        </div>
        <NativeSelect value={scope} onChange={setScope} placeholder="all scopes" options={[{ value: "global", label: "global" }, { value: "project", label: "project" }]} />
        <NativeSelect value={type} onChange={setType} placeholder="all types" options={(data?.types ?? []).map((t) => ({ value: t, label: memoryTypeLabel[t] ?? t }))} />
        {ids.length > 0 && (
          <Badge variant="secondary" className="gap-1">
            {ids.length} selected
            <button onClick={() => router.replace("/memory")}>
              <XIcon className="size-3" />
            </button>
          </Badge>
        )}
        <Button size="sm" className="ml-auto" onClick={() => setOpen(blank(root ? "project" : "global"))}>
          <PlusIcon /> New memory
        </Button>
      </div>

      <ErrorNote error={error} />
      {hits.length === 0 ? (
        <Empty title="No memories here">Memories appear as the agent learns from conversations, or add one by hand.</Empty>
      ) : (
        <div className="rounded-xl border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-[45%]">Memory</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Scope</TableHead>
                <TableHead className="text-right">Priority</TableHead>
                <TableHead>Origin</TableHead>
                <TableHead className="text-right">Recalled</TableHead>
                <TableHead>Updated</TableHead>
                {q && <TableHead className="text-right">Score</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {hits.map(({ record: r, score }) => (
                <TableRow key={r.scope + r.id} className="cursor-pointer" onClick={() => setOpen(r)}>
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
                  <TableCell>
                    <ScopeBadge scope={r.scope} />
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
                  {q && <TableCell className="text-right tabular-nums text-xs">{score.toFixed(2)}</TableCell>}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <RecordSheet
        key={current ? current.scope + current.id + current.version : "none"}
        record={current}
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

function RecordSheet({ record, root, types, onClose }: { record: MemoryRecord | null; root: string; types: string[]; onClose: () => void }) {
  const [r, setR] = React.useState<MemoryRecord | null>(record);
  const [err, setErr] = React.useState<string | null>(null);
  const { data: log } = useFetch<LogEntry[]>(record?.id ? "/api/memory/log" + qs({ root, id: record.id }) : null, [record?.id]);
  if (!r) return null;
  const isNew = !r.id;
  const save = async () => {
    try {
      await api.put("/api/memory", { root, record: r, from: record?.scope });
      toast.success(isNew ? "Memory added" : "Memory saved");
      onClose();
    } catch (e) {
      setErr((e as Error).message);
    }
  };
  const remove = async () => {
    if (!confirm("Delete this memory? The write log keeps a copy.")) return;
    try {
      await api.del("/api/memory" + qs({ root, scope: r.scope, id: r.id }));
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
          <SheetDescription>{isNew ? "Written by hand; origin “manual”." : <Mono>{r.id}</Mono>}</SheetDescription>
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
                options={[{ value: "global", label: "global" }, ...(root ? [{ value: "project", label: "project" }] : [])]}
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
}

function Scenes() {
  const root = useGateway((s) => s.root);
  const v = useVersion("memory");
  const { data } = useFetch<ScopeScenes[]>("/api/memory/scenes" + qs({ root }), [v]);
  return (
    <div className="grid gap-6 xl:grid-cols-2">
      {(data ?? []).map((s) => (
        <div key={s.scope} className="space-y-3">
          <div className="flex items-center gap-2">
            <ScopeBadge scope={s.scope} />
            <span className="text-sm font-medium">{s.scope === "global" ? "About you, everywhere" : "About this project"}</span>
          </div>
          <PersonaCard key={s.persona} scope={s.scope} text={s.persona} root={root} />
          <SceneList scope={s.scope} scenes={s.scenes} root={root} />
        </div>
      ))}
    </div>
  );
}

function PersonaCard({ scope, text, root }: { scope: Scope; text: string; root: string }) {
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
                  await api.put("/api/memory/persona", { root, scope, text: val });
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

function SceneList({ scope, scenes, root }: { scope: Scope; scenes: Scene[]; root: string }) {
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
                await api.del("/api/memory/scenes" + qs({ root, scope, file: s.file })).catch((er) => toast.error(er.message));
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
                  await api.put("/api/memory/scenes", { root, scope, old_file: editing.old, scene: editing });
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
