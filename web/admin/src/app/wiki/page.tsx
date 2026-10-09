"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { ArrowLeftIcon, BookOpenIcon, ClipboardPasteIcon, FileUpIcon, LoaderIcon, LockIcon, PencilIcon, PlayIcon, SearchIcon, Trash2Icon } from "lucide-react";
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
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { baseName } from "@/lib/format";

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
  const pageRef = params.get("page");
  const docRef = params.get("doc");
  // A link from a conversation names its project: follow it.
  const asked = params.get("root");
  const stored = useGateway((s) => s.root);
  const root = asked ?? stored;
  React.useEffect(() => {
    if (asked !== null && asked !== useGateway.getState().root) useGateway.getState().setRoot(asked);
  }, [asked]);
  const [tick, setTick] = React.useState(0);
  const { data, error, reload } = useFetch<WikiData>(`/api/wiki${qs({ root })}`, [tick]);
  const running = !!data?.job.running || !!data?.busy;

  // While an ingest runs, follow it.
  React.useEffect(() => {
    if (!running) return;
    const t = setInterval(() => setTick((n) => n + 1), 2000);
    return () => clearInterval(t);
  }, [running]);

  // Reading is by address, so a link opens a page and Back goes back.
  const go = React.useCallback((p: Record<string, string | undefined>) => router.push(`/wiki${qs({ root: asked ?? undefined, ...p })}`), [router, asked]);
  const openPage = React.useCallback((id: string) => go({ page: id }), [go]);
  const openDoc = React.useCallback((name: string) => go({ doc: name }), [go]);

  const ingest = async () => {
    try {
      const r = await api.post<{ model: string }>("/api/wiki/ingest", { root });
      toast.success(`Ingesting with ${r.model}`);
      reload();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  if (pageRef || docRef) {
    return <Reader data={data} error={error} root={root} pageRef={pageRef} docRef={docRef} onPage={openPage} onDoc={openDoc} onHome={() => go({})} onChanged={reload} onIngest={ingest} />;
  }

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
        <Tabs value={tab} onValueChange={(v) => go({ tab: String(v) })}>
          <TabsList>
            <TabsTrigger value="pages">Pages</TabsTrigger>
            <TabsTrigger value="documents">Documents</TabsTrigger>
            <TabsTrigger value="overview">Overview &amp; lint</TabsTrigger>
            <TabsTrigger value="steering">Purpose &amp; schema</TabsTrigger>
          </TabsList>
          <TabsContent value="pages" className="pt-4">
            {data && <Pages data={data} root={root} onOpen={openPage} />}
          </TabsContent>
          <TabsContent value="documents" className="pt-4">
            {data && <Documents data={data} root={root} onChanged={reload} onOpen={openDoc} />}
          </TabsContent>
          <TabsContent value="overview" className="pt-4">
            {data && <Overview data={data} onOpen={openPage} />}
          </TabsContent>
          <TabsContent value="steering" className="pt-4">
            {data && <Steering data={data} root={root} onSaved={reload} />}
          </TabsContent>
        </Tabs>
      </div>
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
        Add documents or paste text on the Documents tab — or <Mono>agent-tui wiki add docs/</Mono> — then ingest them. In a conversation, ask the agent to put
        something into the wiki, or use <b>Add to wiki</b> on a message or a selection.
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

function Documents({ data, root, onChanged, onOpen }: { data: WikiData; root: string; onChanged: () => void; onOpen: (name: string) => void }) {
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
      <PasteNote root={root} onAdded={onChanged} />
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
                <TableCell className="font-mono text-xs">
                  <button className="text-left hover:underline" onClick={() => onOpen(d.name)} title="Read it, and see the pages it made">
                    {d.name}
                  </button>
                </TableCell>
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

/** PasteNote puts pasted text into the wiki as a document, and reads it into the pages. */
function PasteNote({ root, onAdded }: { root: string; onAdded: () => void }) {
  const [open, setOpen] = React.useState(false);
  const [title, setTitle] = React.useState("");
  const [text, setText] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  if (!open)
    return (
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        <ClipboardPasteIcon /> Paste text
      </Button>
    );
  return (
    <div className="space-y-2 rounded-lg border p-3">
      <Input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Title (optional: the first line otherwise)" />
      <Textarea value={text} onChange={(e) => setText(e.target.value)} rows={8} placeholder="Paste a spec excerpt, a finding, a decision — Markdown is fine." className="text-sm" />
      <div className="flex items-center gap-2">
        <Button
          size="sm"
          disabled={!text.trim() || busy}
          onClick={async () => {
            setBusy(true);
            try {
              const r = await api.post<{ document: string; ingest: string; error?: string }>("/api/wiki/add", { root, title, content: text });
              toast.success(`Added ${r.document}`, { description: r.ingest === "started" ? "Reading it into the pages now." : r.ingest === "queued" ? "An ingest is running; it reads this next." : r.error ?? r.ingest });
              setTitle("");
              setText("");
              setOpen(false);
              onAdded();
            } catch (e) {
              toast.error((e as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          Add and read into pages
        </Button>
        <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
          Cancel
        </Button>
      </div>
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


/**
 * Reader is the wiki read as a wiki: the pages down the side, the one open in
 * a column sized for reading, and what links to it and where it came from
 * beside it. It is reached by address — ?page= or ?doc= — so a link from a
 * conversation opens it, and wikilinks move through history.
 */
function Reader({
  data,
  error,
  root,
  pageRef,
  docRef,
  onPage,
  onDoc,
  onHome,
  onChanged,
  onIngest,
}: {
  data?: WikiData;
  error: string | null;
  root: string;
  pageRef: string | null;
  docRef: string | null;
  onPage: (id: string) => void;
  onDoc: (name: string) => void;
  onHome: () => void;
  onChanged: () => void;
  onIngest: () => void;
}) {
  const [filter, setFilter] = React.useState("");
  const pages = data?.pages ?? [];
  const f = filter.trim().toLowerCase();
  const shown = f ? pages.filter((p) => p.title.toLowerCase().includes(f) || p.description.toLowerCase().includes(f)) : pages;
  const groups = TYPE_ORDER.concat(Array.from(new Set(pages.map((p) => p.type))).filter((t) => !TYPE_ORDER.includes(t)));
  const notes = (data?.documents ?? []).filter((d) => d.name.startsWith("notes/")).slice(-8).reverse();
  const isActive = (p: WikiPage) => pageRef === p.id || pageRef === p.title;
  // A version that moves when an ingest writes, so an open page reloads.
  const version = `${data?.stats.version ?? 0}.${data?.stats.pages ?? 0}.${data?.job.running ? 1 : 0}`;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b px-4 py-2">
        <Button variant="ghost" size="sm" onClick={onHome}>
          <ArrowLeftIcon /> Wiki
        </Button>
        <span className="truncate font-mono text-xs text-muted-foreground" title={root}>
          {root ? baseName(root) : "your own wiki"}
        </span>
        {(data?.job.running || data?.busy) && (
          <span className="ml-auto flex items-center gap-1.5 text-xs text-sky-600 dark:text-sky-400">
            <LoaderIcon className="size-3.5 animate-spin" /> reading documents into pages…
          </span>
        )}
      </div>
      <ErrorNote error={error} className="m-3" />
      <div className="grid min-h-0 flex-1 md:grid-cols-[250px_minmax(0,1fr)] xl:grid-cols-[250px_minmax(0,1fr)_270px]">
        <nav className="hidden min-h-0 flex-col border-r md:flex" aria-label="Wiki pages">
          <div className="p-3 pb-2">
            <div className="relative">
              <SearchIcon className="absolute top-2 left-2 size-4 text-muted-foreground" />
              <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder={`Filter ${pages.length} pages`} className="h-8 pl-8 text-sm" />
            </div>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-4 text-sm">
            {groups
              .filter((t) => shown.some((p) => p.type === t))
              .map((t) => (
                <div key={t} className="mb-3">
                  <div className="px-2 pb-1 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{t}</div>
                  {shown
                    .filter((p) => p.type === t)
                    .sort((a, b) => a.title.localeCompare(b.title))
                    .map((p) => (
                      <button
                        key={p.id}
                        onClick={() => onPage(p.id)}
                        title={p.description}
                        className={cn("block w-full truncate rounded-md px-2 py-1 text-left hover:bg-muted", isActive(p) && "bg-muted font-medium text-foreground")}
                      >
                        {p.title}
                      </button>
                    ))}
                </div>
              ))}
            {notes.length > 0 && (
              <div className="mt-4 border-t pt-3">
                <div className="px-2 pb-1 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">Recent notes</div>
                {notes.map((d) => (
                  <button
                    key={d.name}
                    onClick={() => onDoc(d.name)}
                    title={d.name}
                    className={cn("block w-full truncate rounded-md px-2 py-1 text-left text-xs hover:bg-muted", docRef === d.name && "bg-muted font-medium")}
                  >
                    {noteTitle(d.name)}
                  </button>
                ))}
              </div>
            )}
          </div>
        </nav>
        {pageRef ? (
          <PageArticle key={pageRef} root={root} pageRef={pageRef} version={version} docs={data?.documents ?? []} onPage={onPage} onDoc={onDoc} onChanged={onChanged} />
        ) : (
          docRef && <DocArticle key={docRef} root={root} docRef={docRef} data={data} onPage={onPage} onIngest={onIngest} />
        )}
      </div>
    </div>
  );
}

/** noteTitle names a note from its file: notes/20261009-160223-payment-retry.md → "payment retry · 10-09 16:02". */
function noteTitle(name: string): string {
  const m = /^notes\/(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})\d{2}-(.*)\.md$/.exec(name);
  if (!m) return name.replace(/^notes\//, "");
  return `${m[6].replace(/-/g, " ")} · ${m[2]}-${m[3]} ${m[4]}:${m[5]}`;
}

// The column a page is read in: wide enough for a table, narrow enough to read.
const ARTICLE = "mx-auto w-full max-w-3xl px-6 py-8 md:px-10";
const PROSE = "text-[15px] leading-7 [&_h1]:mt-8 [&_h2]:mt-8 [&_h2]:border-b [&_h2]:pb-1 [&_h3]:mt-6 [&_table]:text-[13px]";

function PageArticle({
  root,
  pageRef,
  version,
  docs,
  onPage,
  onDoc,
  onChanged,
}: {
  root: string;
  pageRef: string;
  version: string;
  docs: WikiDoc[];
  onPage: (id: string) => void;
  onDoc: (name: string) => void;
  onChanged: () => void;
}) {
  const { data, error, reload } = useFetch<PageView>(`/api/wiki/page${qs({ root, id: pageRef })}`, [version]);
  const [edit, setEdit] = React.useState<{ title: string; description: string; body: string } | null>(null);
  const p = data?.page;
  const related = data?.related ?? [];
  const inbound = related.filter((n) => n.dir !== "out");
  const outbound = related.filter((n) => n.dir !== "in");
  const docNames = new Set(docs.map((d) => d.name));

  if (error)
    return (
      <main className="min-h-0 overflow-y-auto">
        <div className={ARTICLE}>
          <Empty title={`No page “${pageRef}”`}>It may not be written yet: an ingest still running writes it in a minute.</Empty>
        </div>
      </main>
    );
  return (
    <>
      <main className="min-h-0 overflow-y-auto">
        <article className={ARTICLE}>
          {!p ? (
            <div className="text-sm text-muted-foreground">Loading…</div>
          ) : edit ? (
            <div className="space-y-3">
              <Input value={edit.title} onChange={(e) => setEdit({ ...edit, title: e.target.value })} className="text-lg font-semibold" />
              <Input value={edit.description} onChange={(e) => setEdit({ ...edit, description: e.target.value })} placeholder="One sentence: what the page is about" />
              <Textarea value={edit.body} onChange={(e) => setEdit({ ...edit, body: e.target.value })} rows={24} className="font-mono text-xs" />
              <p className="text-xs text-muted-foreground">A page saved here is locked: later ingests will not merge into it.</p>
              <div className="flex gap-2">
                <Button
                  size="sm"
                  onClick={async () => {
                    try {
                      const r = await api.put<{ id: string }>("/api/wiki/page", { root, id: p.id, type: p.type, ...edit });
                      setEdit(null);
                      onChanged();
                      if (r.id !== p.id) onPage(r.id);
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
          ) : (
            <>
              <div className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
                <Badge variant="outline">{p.type}</Badge>
                <span className="font-mono">{p.id}</span>
                {p.locked && (
                  <span className="flex items-center gap-1" title="Edited by hand: ingests do not merge into it">
                    <LockIcon className="size-3" /> locked
                  </span>
                )}
              </div>
              <h1 className="text-2xl leading-tight font-semibold tracking-tight">{p.title}</h1>
              {p.description && <p className="mt-2 text-[15px] text-muted-foreground">{p.description}</p>}
              <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 border-b pb-4 text-xs text-muted-foreground">
                {p.updated && <span>updated {p.updated}</span>}
                {data.history > 0 && <span>{data.history} earlier version{data.history > 1 ? "s" : ""} kept</span>}
                <span className="ml-auto flex gap-1">
                  <Button variant="ghost" size="sm" onClick={() => setEdit({ title: p.title, description: p.description, body: data.body })}>
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
                        history.back();
                      } catch (e) {
                        toast.error((e as Error).message);
                      }
                    }}
                  >
                    <Trash2Icon /> Delete
                  </Button>
                </span>
              </div>
              <div className={cn("mt-6", PROSE)}>
                <WikiMarkdown text={data.body} onOpen={onPage} />
              </div>
              {!!data.broken?.length && (
                <p className="mt-8 text-xs text-muted-foreground">Links to pages not written yet: {data.broken.map((b) => `[[${b}]]`).join(", ")}</p>
              )}
            </>
          )}
        </article>
      </main>
      <aside className="hidden min-h-0 space-y-6 overflow-y-auto border-l p-4 text-sm xl:block">
        {p && (
          <>
            <RelatedList title="Links here" items={inbound} onPage={onPage} empty="Nothing links here yet." />
            <RelatedList title="Links to" items={outbound} onPage={onPage} empty="No links out." />
            <div>
              <div className="mb-1.5 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">Sources</div>
              {(p.sources ?? []).map((s) =>
                docNames.has(s) ? (
                  <button key={s} onClick={() => onDoc(s)} title={s} className="block w-full truncate rounded-md px-2 py-1 text-left text-xs text-primary hover:bg-muted">
                    {s.startsWith("notes/") ? noteTitle(s) : s}
                  </button>
                ) : (
                  <div key={s} className="px-2 py-1 text-xs text-muted-foreground">
                    {s === "agent" ? "filed by the agent" : s === "user" ? "edited by hand" : s}
                  </div>
                ),
              )}
            </div>
          </>
        )}
      </aside>
    </>
  );
}

function RelatedList({ title, items, onPage, empty }: { title: string; items: Neighbour[]; onPage: (id: string) => void; empty: string }) {
  return (
    <div>
      <div className="mb-1.5 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
        {title} <span className="tabular-nums">{items.length || ""}</span>
      </div>
      {items.length === 0 && <div className="px-2 text-xs text-muted-foreground">{empty}</div>}
      {items.map((n) => (
        <button key={n.id} onClick={() => onPage(n.id)} className="block w-full truncate rounded-md px-2 py-1 text-left hover:bg-muted">
          {n.title}
        </button>
      ))}
    </div>
  );
}

/** DocArticle is one document: what it says, and the pages it was read into — or that it is being read. */
function DocArticle({ root, docRef, data, onPage, onIngest }: { root: string; docRef: string; data?: WikiData; onPage: (id: string) => void; onIngest: () => void }) {
  const doc = data?.documents.find((d) => d.name === docRef);
  const { data: raw, error } = useFetch<{ content: string }>(`/api/wiki/raw${qs({ root, name: docRef })}`, []);
  const reading = !!data?.job.running && doc?.status !== "ingested";
  const made = (doc?.pages ?? []).map((id) => data?.pages.find((p) => p.id === id)).filter((p): p is WikiPage => !!p);
  return (
    <>
      <main className="min-h-0 overflow-y-auto">
        <article className={ARTICLE}>
          <div className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
            <Badge variant="outline">document</Badge>
            <span className="truncate font-mono">{docRef}</span>
          </div>
          <ErrorNote error={error} />
          {reading && (
            <div className="my-4 rounded-lg border bg-muted/40 p-3 text-sm">
              <div className="flex items-center gap-2 font-medium">
                <LoaderIcon className="size-4 animate-spin" /> Being read into the wiki&apos;s pages…
              </div>
              <Pre max="max-h-32" className="mt-2">
                {(data?.job.progress ?? []).slice(-5).join("\n")}
              </Pre>
            </div>
          )}
          {made.length > 0 && (
            <div className="my-4 rounded-lg border p-3">
              <div className="mb-2 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">Read into {made.length} pages</div>
              <div className="flex flex-wrap gap-1.5">
                {made.map((p) => (
                  <Button key={p.id} variant="outline" size="sm" onClick={() => onPage(p.id)} title={p.description}>
                    {p.title}
                  </Button>
                ))}
              </div>
            </div>
          )}
          {doc && !reading && (doc.status === "new" || doc.status === "changed") && (
            <div className="my-4 flex items-center gap-3 rounded-lg border bg-muted/40 p-3 text-sm">
              <span className="flex-1">Not read into the wiki&apos;s pages yet.</span>
              <Button size="sm" onClick={onIngest} disabled={!!data?.busy}>
                <PlayIcon /> Ingest now
              </Button>
            </div>
          )}
          {doc?.status === "failed" && <ErrorNote error={`Reading it failed: ${doc.error}`} />}
          <div className={cn("mt-6", PROSE)}>{raw ? <Markdown text={raw.content} /> : <div className="text-sm text-muted-foreground">Loading…</div>}</div>
        </article>
      </main>
      <aside className="hidden min-h-0 space-y-2 overflow-y-auto border-l p-4 text-xs text-muted-foreground xl:block">
        {doc && (
          <>
            <div>
              status <Badge variant={doc.status === "ingested" ? "secondary" : doc.status === "failed" ? "destructive" : "outline"}>{reading ? "reading" : doc.status}</Badge>
            </div>
            <div>{(doc.size / 1024).toFixed(1)} KB</div>
            {doc.ingested && (
              <div>
                read <Ago at={doc.ingested} />
              </div>
            )}
          </>
        )}
      </aside>
    </>
  );
}
