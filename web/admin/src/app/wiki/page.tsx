"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { BookOpenIcon, FileUpIcon, LockIcon, PencilIcon, PlayIcon, SearchIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useGateway } from "@/lib/store";
import { Ago, Empty, ErrorNote, Mono, PageHeader, Pre, Stat } from "@/components/common";
import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

type WikiPage = {
  id: string;
  type: string;
  title: string;
  description: string;
  sources: string[] | null;
  tags: string[] | null;
  updated: string;
  locked: boolean;
  links: number;
  backlinks: number;
};

type WikiDoc = { name: string; size: number; sha256: string; status: string; error?: string; pages: string[]; ingested?: string };

type Report = { ingested: string[] | null; skipped: number; deleted: string[] | null; failed: Record<string, string>; written: string[] | null; removed: string[] | null };

type Job = { running: boolean; started: string; model?: string; progress: string[]; report?: Report; error?: string };

type Lint = { broken: Record<string, string[]>; orphans: string[] | null; no_source: string[] | null; failed: Record<string, string> };

type WikiData = {
  stats: { dir: string; pages: number; by_type: Record<string, number>; raw: number; pending: number; failed: number; version: number; ingested?: string };
  pages: WikiPage[];
  documents: WikiDoc[];
  overview: string;
  lint: Lint;
  job: Job;
  busy: boolean;
  purpose: string;
  schema: string;
  types: string[];
};

type Neighbour = { id: string; title: string; dir: string };
type Result = { id: string; title: string; type: string; snippet: string; score: number; hop: number; via?: string; related?: Neighbour[] };
type PageView = { page: WikiPage; body: string; related: Neighbour[] | null; broken: string[] | null; history: number };

const TYPE_ORDER = ["source", "entity", "concept", "comparison", "synthesis"];

// [[Title]] and [[Title|label]] become links the page catches, so a click
// opens the page here rather than a new tab.
function linkify(body: string): string {
  return body.replace(/\[\[([^\[\]|\n]+)(?:\|([^\[\]\n]*))?\]\]/g, (_, t: string, label?: string) => {
    const text = (label || t).trim().replace(/[[\]]/g, "");
    return `[${text}](#wiki:${encodeURIComponent(t.trim())})`;
  });
}

function WikiMarkdown({ text, onOpen }: { text: string; onOpen: (ref: string) => void }) {
  return (
    <div
      onClickCapture={(e) => {
        const a = (e.target as HTMLElement).closest("a");
        const href = a?.getAttribute("href") ?? "";
        if (href.startsWith("#wiki:")) {
          e.preventDefault();
          e.stopPropagation();
          onOpen(decodeURIComponent(href.slice(6)));
        }
      }}
    >
      <Markdown text={linkify(text)} />
    </div>
  );
}

export default function WikiRoute() {
  return (
    <React.Suspense fallback={null}>
      <Wiki />
    </React.Suspense>
  );
}

function Wiki() {
  const params = useSearchParams();
  const router = useRouter();
  const tab = params.get("tab") ?? "pages";
  const root = useGateway((s) => s.root);
  const [tick, setTick] = React.useState(0);
  const { data, error, reload } = useFetch<WikiData>(`/api/wiki${qs({ root })}`, [tick]);
  const [open, setOpen] = React.useState<string | null>(null);
  const running = !!data?.job.running || !!data?.busy;

  // While an ingest runs, follow it.
  React.useEffect(() => {
    if (!running) return;
    const t = setInterval(() => setTick((n) => n + 1), 2000);
    return () => clearInterval(t);
  }, [running]);

  const ingest = async () => {
    try {
      const r = await api.post<{ model: string }>("/api/wiki/ingest", { root });
      toast.success(`Ingesting with ${r.model}`);
      reload();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const s = data?.stats;
  return (
    <div>
      <PageHeader
        title="Wiki"
        description={
          <>
            Documents read once by a model and kept as linked pages — the LLM wiki of TencentDB&apos;s MemoryKnowledge. The agent searches it with <Mono>wiki_search</Mono> and reads
            pages with <Mono>wiki_read</Mono>; the overview is in every prompt. {root ? <>Wiki of <Mono>{root}</Mono>.</> : <>No project picked: this is your own wiki.</>}
          </>
        }
        actions={
          <Button size="sm" disabled={running || !s?.pending} onClick={ingest} title="Read new and changed documents into pages">
            <PlayIcon /> {running ? "Ingesting…" : s?.pending ? `Ingest ${s.pending} document${s.pending > 1 ? "s" : ""}` : "Nothing to ingest"}
          </Button>
        }
      />
      <div className="space-y-6 p-6">
        <ErrorNote error={error} />
        {s && (
          <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
            <Stat label="Pages" value={s.pages} hint={TYPE_ORDER.filter((t) => s.by_type[t]).map((t) => `${s.by_type[t]} ${t}`).join(" · ")} />
            <Stat label="Documents" value={s.raw} />
            <Stat label="To ingest" value={s.pending} />
            <Stat label="Failed" value={s.failed} />
            <Stat label="Last ingest" value={s.ingested ? <Ago at={s.ingested} /> : "never"} hint={s.version ? `version ${s.version}` : undefined} />
          </div>
        )}
        {data && (data.job.running || data.job.report || data.job.error) && <JobCard job={data.job} />}
        <Tabs value={tab} onValueChange={(v) => router.replace(`/wiki?tab=${v}`)}>
          <TabsList>
            <TabsTrigger value="pages">Pages</TabsTrigger>
            <TabsTrigger value="documents">Documents</TabsTrigger>
            <TabsTrigger value="overview">Overview &amp; lint</TabsTrigger>
            <TabsTrigger value="steering">Purpose &amp; schema</TabsTrigger>
          </TabsList>
          <TabsContent value="pages" className="pt-4">
            {data && <Pages data={data} root={root} onOpen={setOpen} />}
          </TabsContent>
          <TabsContent value="documents" className="pt-4">
            {data && <Documents data={data} root={root} onChanged={reload} />}
          </TabsContent>
          <TabsContent value="overview" className="pt-4">
            {data && <Overview data={data} onOpen={setOpen} />}
          </TabsContent>
          <TabsContent value="steering" className="pt-4">
            {data && <Steering data={data} root={root} onSaved={reload} />}
          </TabsContent>
        </Tabs>
      </div>
      <PageSheet root={root} refId={open} onOpen={setOpen} onClose={() => setOpen(null)} onChanged={reload} />
    </div>
  );
}

function JobCard({ job }: { job: Job }) {
  const r = job.report;
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm">{job.running ? "Ingesting" : job.error ? "Last ingest failed" : "Last ingest"}</CardTitle>
        <CardDescription>
          {job.model} · started <Ago at={job.started} />
          {r && !job.running && (
            <>
              {" "}
              · read {r.ingested?.length ?? 0}, unchanged {r.skipped}, removed {r.deleted?.length ?? 0} · wrote {r.written?.length ?? 0} pages, took out {r.removed?.length ?? 0}
            </>
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-2">
        <ErrorNote error={job.error} />
        {r && Object.entries(r.failed ?? {}).map(([what, why]) => (
          <div key={what} className="text-xs text-destructive">
            <Mono>{what}</Mono>: {why}
          </div>
        ))}
        {job.progress.length > 0 && <Pre max="max-h-48">{job.progress.slice(-40).join("\n")}</Pre>}
      </CardContent>
    </Card>
  );
}

function Pages({ data, root, onOpen }: { data: WikiData; root: string; onOpen: (id: string) => void }) {
  const [q, setQ] = React.useState("");
  const [hops, setHops] = React.useState(1);
  const search = useFetch<Result[]>(q.trim() ? `/api/wiki/search${qs({ root, q: q.trim(), hops })}` : null, []);
  if (data.pages.length === 0) {
    return (
      <Empty title="No pages yet">
        Add documents on the Documents tab — or <Mono>agent-tui wiki add docs/</Mono> — then ingest them.
      </Empty>
    );
  }
  const groups = TYPE_ORDER.concat(Object.keys(data.stats.by_type).filter((t) => !TYPE_ORDER.includes(t)));
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <SearchIcon className="absolute top-2.5 left-2.5 size-4 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search the wiki as the agent does…" className="pl-8" />
        </div>
        <select className="h-9 rounded-md border bg-transparent px-2 text-sm" value={hops} onChange={(e) => setHops(Number(e.target.value))} title="Also show pages this many links away">
          <option value={0}>matches only</option>
          <option value={1}>+1 link</option>
          <option value={2}>+2 links</option>
        </select>
      </div>
      {q.trim() ? (
        <div className="space-y-2">
          <ErrorNote error={search.error} />
          {search.data?.length === 0 && <Empty title="Nothing found" />}
          {search.data?.map((r) => (
            <button key={r.id} onClick={() => onOpen(r.id)} className="block w-full rounded-lg border p-3 text-left hover:bg-muted/50">
              <div className="flex items-center gap-2">
                <span className="font-medium">{r.title}</span>
                <Badge variant="outline">{r.type}</Badge>
                {r.hop > 0 && <span className="text-xs text-muted-foreground">via {r.via}</span>}
                <span className="ml-auto font-mono text-xs text-muted-foreground">{r.score.toFixed(2)}</span>
              </div>
              <div className="mt-1 text-sm text-muted-foreground">{r.snippet}</div>
            </button>
          ))}
        </div>
      ) : (
        groups
          .filter((t) => data.pages.some((p) => p.type === t))
          .map((t) => (
            <div key={t}>
              <div className="mb-2 text-xs font-medium tracking-wide text-muted-foreground uppercase">
                {t} · {data.pages.filter((p) => p.type === t).length}
              </div>
              <Table>
                <TableBody>
                  {data.pages
                    .filter((p) => p.type === t)
                    .sort((a, b) => a.title.localeCompare(b.title))
                    .map((p) => (
                      <TableRow key={p.id} className="cursor-pointer" onClick={() => onOpen(p.id)}>
                        <TableCell className="font-medium whitespace-nowrap">
                          {p.locked && <LockIcon className="mr-1 inline size-3 text-muted-foreground" />}
                          {p.title}
                        </TableCell>
                        <TableCell className="w-full max-w-0 truncate text-muted-foreground" title={p.description}>
                          {p.description}
                        </TableCell>
                        <TableCell className="text-right text-xs whitespace-nowrap text-muted-foreground">
                          {p.links} → · {p.backlinks} ←
                        </TableCell>
                      </TableRow>
                    ))}
                </TableBody>
              </Table>
            </div>
          ))
      )}
    </div>
  );
}

function Documents({ data, root, onChanged }: { data: WikiData; root: string; onChanged: () => void }) {
  const input = React.useRef<HTMLInputElement>(null);
  const upload = async (files: FileList | null) => {
    if (!files?.length) return;
    const list = await Promise.all(Array.from(files).map(async (f) => ({ name: f.name, content: await f.text() })));
    try {
      const r = await api.post<{ added: string[]; failed: Record<string, string> }>("/api/wiki/raw", { root, files: list });
      if (r.added.length) toast.success(`${r.added.length} document${r.added.length > 1 ? "s" : ""} added — ingest to read them`);
      for (const [n, why] of Object.entries(r.failed)) toast.error(`${n}: ${why}`);
      onChanged();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  const statusVariant = (s: string) => (s === "ingested" ? "secondary" : s === "failed" ? "destructive" : "outline");
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <input ref={input} type="file" multiple accept=".md,.markdown,.txt,.rst,.adoc,.org,.csv" className="hidden" onChange={(e) => upload(e.target.files)} />
        <Button variant="outline" size="sm" onClick={() => input.current?.click()}>
          <FileUpIcon /> Add documents
        </Button>
        <span className="text-xs text-muted-foreground">
          Text and Markdown, up to 2 MB each. Convert PDF and Word to Markdown first. A folder: <Mono>agent-tui wiki add docs/</Mono>
        </span>
      </div>
      {data.documents.length === 0 ? (
        <Empty title="No documents" />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Document</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Pages</TableHead>
              <TableHead>Size</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.documents.map((d) => (
              <TableRow key={d.name}>
                <TableCell className="font-mono text-xs">{d.name}</TableCell>
                <TableCell>
                  <Badge variant={statusVariant(d.status)} title={d.error}>
                    {d.status}
                  </Badge>
                  {d.ingested && (
                    <span className="ml-2 text-xs text-muted-foreground">
                      <Ago at={d.ingested} />
                    </span>
                  )}
                </TableCell>
                <TableCell>{d.pages.length}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{(d.size / 1024).toFixed(1)} KB</TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="ghost"
                    size="icon"
                    title="Remove — the next ingest takes out the pages only this document gave"
                    onClick={async () => {
                      if (!confirm(`Remove ${d.name}? The next ingest takes out the pages only it gave.`)) return;
                      try {
                        await api.del(`/api/wiki/raw${qs({ root, name: d.name })}`);
                        onChanged();
                      } catch (e) {
                        toast.error((e as Error).message);
                      }
                    }}
                  >
                    <Trash2Icon />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

function Overview({ data, onOpen }: { data: WikiData; onOpen: (id: string) => void }) {
  const l = data.lint;
  const broken = Object.entries(l.broken ?? {});
  return (
    <div className="grid gap-6 lg:grid-cols-[2fr_1fr]">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-sm">
            <BookOpenIcon className="size-4" /> Overview
          </CardTitle>
          <CardDescription>Written by the model after each ingest; the agent sees it in every prompt.</CardDescription>
        </CardHeader>
        <CardContent>{data.overview ? <WikiMarkdown text={data.overview} onOpen={onOpen} /> : <Empty title="No overview yet" />}</CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Lint</CardTitle>
          <CardDescription>Found without a model.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          {!broken.length && !l.orphans?.length && !Object.keys(l.failed ?? {}).length && <div className="text-muted-foreground">Nothing to fix.</div>}
          {broken.map(([id, links]) => (
            <div key={id}>
              <button className="text-primary underline" onClick={() => onOpen(id)}>
                {id}
              </button>{" "}
              links to missing {links.map((x) => `[[${x}]]`).join(", ")}
            </div>
          ))}
          {!!l.orphans?.length && (
            <div>
              <div className="text-xs text-muted-foreground">Nothing links to</div>
              {l.orphans.map((id) => (
                <button key={id} className="mr-2 text-primary underline" onClick={() => onOpen(id)}>
                  {id}
                </button>
              ))}
            </div>
          )}
          {Object.entries(l.failed ?? {}).map(([n, why]) => (
            <div key={n} className="text-destructive">
              <Mono>{n}</Mono>: {why}
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}

function Steering({ data, root, onSaved }: { data: WikiData; root: string; onSaved: () => void }) {
  const [purpose, setPurpose] = React.useState(data.purpose);
  const [schema, setSchema] = React.useState(data.schema);
  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">Every ingest reads these two files: what the wiki is for, and what to extract. Changing them does not rewrite pages already written.</p>
      <div className="grid gap-4 lg:grid-cols-2">
        <div className="space-y-1">
          <div className="text-sm font-medium">purpose.md</div>
          <Textarea value={purpose} onChange={(e) => setPurpose(e.target.value)} rows={16} className="font-mono text-xs" />
        </div>
        <div className="space-y-1">
          <div className="text-sm font-medium">schema.md</div>
          <Textarea value={schema} onChange={(e) => setSchema(e.target.value)} rows={16} className="font-mono text-xs" />
        </div>
      </div>
      <Button
        size="sm"
        onClick={async () => {
          try {
            await api.put("/api/wiki/steer", { root, purpose, schema });
            toast.success("Saved");
            onSaved();
          } catch (e) {
            toast.error((e as Error).message);
          }
        }}
      >
        Save
      </Button>
    </div>
  );
}

function PageSheet({ root, refId, onOpen, onClose, onChanged }: { root: string; refId: string | null; onOpen: (id: string) => void; onClose: () => void; onChanged: () => void }) {
  const { data, error, reload } = useFetch<PageView>(refId ? `/api/wiki/page${qs({ root, id: refId })}` : null, []);
  // The draft belongs to the page it was started on; opening another drops it.
  const [draft, setDraft] = React.useState<{ ref: string | null; title: string; description: string; body: string } | null>(null);
  const edit = draft && draft.ref === refId ? draft : null;
  const setEdit = (e: { title: string; description: string; body: string } | null) => setDraft(e && { ...e, ref: refId });
  const p = data?.page;
  return (
    <Sheet open={!!refId} onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-2xl">
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2">
            {p?.locked && <LockIcon className="size-4 text-muted-foreground" />}
            {p?.title ?? refId}
          </SheetTitle>
          <SheetDescription>
            {p && (
              <>
                {p.type} · <Mono>{p.id}</Mono> · from {(p.sources ?? []).join(", ") || "nowhere"}
                {p.updated && <> · updated {p.updated}</>}
                {data?.history ? <> · {data.history} earlier version{data.history > 1 ? "s" : ""} kept</> : null}
              </>
            )}
          </SheetDescription>
        </SheetHeader>
        <div className="space-y-4 px-4 pb-6">
          <ErrorNote error={error} />
          {p && !edit && (
            <>
              {p.description && <p className="text-sm text-muted-foreground">{p.description}</p>}
              <WikiMarkdown text={data!.body} onOpen={onOpen} />
              {!!data!.related?.length && (
                <div className="border-t pt-3">
                  <div className="mb-1 text-xs text-muted-foreground">Linked pages</div>
                  <div className="flex flex-wrap gap-1.5">
                    {data!.related!.map((n) => (
                      <Button key={n.id} variant="outline" size="sm" onClick={() => onOpen(n.id)} title={n.dir === "in" ? "links here" : n.dir === "out" ? "linked from here" : "both ways"}>
                        {n.dir === "in" ? "← " : n.dir === "out" ? "→ " : "↔ "}
                        {n.title}
                      </Button>
                    ))}
                  </div>
                </div>
              )}
              <div className="flex gap-2 border-t pt-3">
                <Button variant="outline" size="sm" onClick={() => setEdit({ title: p.title, description: p.description, body: data!.body })}>
                  <PencilIcon /> Edit
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={async () => {
                    if (!confirm(`Delete ${p.title}? A copy stays in history/.`)) return;
                    try {
                      await api.del(`/api/wiki/page${qs({ root, id: p.id })}`);
                      onChanged();
                      onClose();
                    } catch (e) {
                      toast.error((e as Error).message);
                    }
                  }}
                >
                  <Trash2Icon /> Delete
                </Button>
              </div>
            </>
          )}
          {p && edit && (
            <div className="space-y-3">
              <Input value={edit.title} onChange={(e) => setEdit({ ...edit, title: e.target.value })} />
              <Input value={edit.description} onChange={(e) => setEdit({ ...edit, description: e.target.value })} placeholder="One sentence: what the page is about" />
              <Textarea value={edit.body} onChange={(e) => setEdit({ ...edit, body: e.target.value })} rows={20} className="font-mono text-xs" />
              <p className="text-xs text-muted-foreground">A page saved here is locked: later ingests will not merge into it.</p>
              <div className="flex gap-2">
                <Button
                  size="sm"
                  onClick={async () => {
                    try {
                      const r = await api.put<{ id: string }>("/api/wiki/page", { root, id: p.id, type: p.type, ...edit });
                      setEdit(null);
                      onChanged();
                      if (r.id !== p.id) onOpen(r.id);
                      else reload();
                    } catch (e) {
                      toast.error((e as Error).message);
                    }
                  }}
                >
                  Save
                </Button>
                <Button variant="ghost" size="sm" onClick={() => setEdit(null)}>
                  Cancel
                </Button>
              </div>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
