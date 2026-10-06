"use client";

import * as React from "react";
import { ChevronRightIcon, FileIcon, FolderIcon, FolderOpenIcon, ArrowLeftIcon, SearchIcon, RefreshCwIcon, ExternalLinkIcon } from "lucide-react";
import { toast } from "sonner";
import { api, gatewayBase, qs } from "@/lib/api";
import { cn } from "@/lib/utils";
import { CopyButton } from "@/components/common";
import { Markdown } from "@/components/markdown";
import { Input } from "@/components/ui/input";

export { FileOpener, useFileOpener, looksLikePath } from "@/lib/file-opener";

// The project explorer: a session's project, browsed and read from the web,
// and the place a path in the conversation opens when it is clicked.

type Entry = { name: string; path: string; dir: boolean; size?: number };
type FileData = { path: string; size: number; text?: string; binary: boolean; truncated: boolean };

const imageExt = /\.(png|jpe?g|gif|webp|svg)$/i;

export function lineOf(p: string): number | undefined {
  const m = p.match(/:(\d+)(?::\d+)?$/);
  return m ? Number(m[1]) : undefined;
}

function Tree({ root, dir, depth, selected, open, onPick }: { root: string; dir: string; depth: number; selected?: string; open: Set<string>; onPick: (e: Entry) => void }) {
  const [entries, setEntries] = React.useState<Entry[] | null>(null);
  const [err, setErr] = React.useState<string | null>(null);
  React.useEffect(() => {
    let live = true;
    api
      .get<{ entries: Entry[] }>("/api/files/list" + qs({ root, dir }))
      .then((d) => live && setEntries(d.entries))
      .catch((e) => live && setErr((e as Error).message));
    return () => {
      live = false;
    };
  }, [root, dir]);
  if (err) return <div className="px-2 py-1 text-xs text-destructive">{err}</div>;
  if (!entries) return <div className="px-2 py-1 text-xs text-muted-foreground" style={{ paddingLeft: depth * 12 + 8 }}>…</div>;
  return (
    <div>
      {entries.map((e) => (
        <div key={e.path}>
          <button
            onClick={() => onPick(e)}
            className={cn("flex w-full min-w-0 items-center gap-1 rounded px-1 py-0.5 text-left text-[13px] hover:bg-muted/60", selected === e.path && "bg-primary/10 text-primary")}
            style={{ paddingLeft: depth * 12 + 4 }}
            title={e.path}
          >
            {e.dir ? (
              <>
                <ChevronRightIcon className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", open.has(e.path) && "rotate-90")} />
                {open.has(e.path) ? <FolderOpenIcon className="size-3.5 shrink-0 text-sky-600" /> : <FolderIcon className="size-3.5 shrink-0 text-sky-600" />}
              </>
            ) : (
              <>
                <span className="w-3.5 shrink-0" />
                <FileIcon className="size-3.5 shrink-0 text-muted-foreground" />
              </>
            )}
            <span className="truncate">{e.name}</span>
          </button>
          {e.dir && open.has(e.path) && <Tree root={root} dir={e.path} depth={depth + 1} selected={selected} open={open} onPick={onPick} />}
        </div>
      ))}
      {entries.length === 0 && (
        <div className="py-0.5 text-xs text-muted-foreground" style={{ paddingLeft: depth * 12 + 24 }}>
          empty
        </div>
      )}
    </div>
  );
}

function FileView({ root, path, line, onBack }: { root: string; path: string; line?: number; onBack: () => void }) {
  const [data, setData] = React.useState<FileData | null>(null);
  const [err, setErr] = React.useState<string | null>(null);
  const [raw, setRaw] = React.useState(false);
  const box = React.useRef<HTMLDivElement>(null);
  const isImage = imageExt.test(path);
  const isMd = /\.mdx?$/i.test(path);
  React.useEffect(() => {
    if (isImage) return;
    let live = true;
    api
      .get<FileData>("/api/files/read" + qs({ root, path }))
      .then((d) => live && setData(d))
      .catch((e) => live && setErr((e as Error).message));
    return () => {
      live = false;
    };
  }, [root, path, isImage]);
  React.useEffect(() => {
    if (!line || !data) return;
    box.current?.querySelector(`[data-line="${line}"]`)?.scrollIntoView({ block: "center" });
  }, [line, data, raw]);
  const showRaw = raw || !isMd || line !== undefined;
  const lines = data?.text?.split("\n") ?? [];
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center gap-1.5 border-b px-2 py-1.5 text-xs">
        <button onClick={onBack} className="rounded p-1 hover:bg-muted" title="Back to the tree">
          <ArrowLeftIcon className="size-3.5" />
        </button>
        <span className="min-w-0 flex-1 truncate font-mono" title={path}>
          {path}
          {line ? `:${line}` : ""}
        </span>
        {isMd && data?.text !== undefined && (
          <button onClick={() => setRaw(!raw)} className="rounded px-1.5 py-0.5 text-muted-foreground hover:bg-muted hover:text-foreground">
            {showRaw ? "rendered" : "source"}
          </button>
        )}
        <CopyButton text={path} />
        <a href={gatewayBase() + "/api/files/raw" + qs({ root, path })} target="_blank" rel="noreferrer" className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground" title="Open raw">
          <ExternalLinkIcon className="size-3.5" />
        </a>
      </div>
      <div ref={box} className="min-h-0 flex-1 overflow-auto">
        {err && <div className="p-3 text-sm text-destructive">{err}</div>}
        {isImage ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={gatewayBase() + "/api/files/raw" + qs({ root, path })} alt={path} className="m-3 max-w-[calc(100%-1.5rem)]" />
        ) : !data ? (
          !err && <div className="p-3 text-sm text-muted-foreground">Loading…</div>
        ) : data.binary ? (
          <div className="p-3 text-sm text-muted-foreground">A binary file ({data.size} bytes).</div>
        ) : !showRaw ? (
          <div className="p-4 text-sm">
            <Markdown text={data.text ?? ""} />
          </div>
        ) : (
          <div className="font-mono text-[12px] leading-[1.55]">
            {lines.map((l, i) => (
              <div key={i} data-line={i + 1} className={cn("flex", line === i + 1 && "bg-amber-500/15")}>
                <span className="w-12 shrink-0 pr-3 text-right text-muted-foreground/60 select-none">{i + 1}</span>
                <span className="whitespace-pre-wrap break-all pr-3">{l || " "}</span>
              </div>
            ))}
            {data.truncated && <div className="p-3 text-xs text-muted-foreground">… only the first 1 MB is shown.</div>}
          </div>
        )}
      </div>
    </div>
  );
}

function ancestors(p: string): string[] {
  const parts = p.split("/");
  return parts.slice(0, -1).map((_, i) => parts.slice(0, i + 1).join("/"));
}

/**
 * Explorer is the project's tree, a quick file search, and the file open in
 * it. `request` is a path someone asked to see (from the conversation): it is
 * resolved to a project file, and opened.
 */
export function Explorer({ root, cwd, request }: { root: string; cwd?: string; request?: { path: string; seq: number } }) {
  const [open, setOpen] = React.useState<Set<string>>(new Set());
  const [file, setFile] = React.useState<{ path: string; line?: number } | null>(null);
  const [q, setQ] = React.useState("");
  const [found, setFound] = React.useState<string[] | null>(null);
  const [pick, setPick] = React.useState<string[] | null>(null);
  const [gen, setGen] = React.useState(0);

  const show = React.useCallback((path: string, line?: number) => {
    setFile({ path, line });
    setPick(null);
    setOpen((o) => new Set([...o, ...ancestors(path)]));
  }, []);

  // A path from the conversation: resolved by the gateway, then opened.
  React.useEffect(() => {
    if (!request) return;
    let live = true;
    api
      .get<{ path?: string; candidates?: string[] }>("/api/files/resolve" + qs({ root, cwd, p: request.path }))
      .then((r) => {
        if (!live) return;
        if (r.path) show(r.path, lineOf(request.path));
        else if (r.candidates?.length) {
          setFile(null);
          setPick(r.candidates);
        } else toast.error(`${request.path} is not in this project`);
      })
      .catch((e) => toast.error((e as Error).message));
    return () => {
      live = false;
    };
  }, [request, root, cwd, show]);

  React.useEffect(() => {
    const t = q.trim();
    if (!t) return;
    let live = true;
    const h = setTimeout(() => {
      api
        .get<{ files: string[] }>("/api/files/find" + qs({ root, q: t }))
        .then((r) => live && setFound(r.files))
        .catch(() => live && setFound([]));
    }, 200);
    return () => {
      live = false;
      clearTimeout(h);
    };
  }, [q, root]);

  if (file) return <FileView key={file.path + (file.line ?? "")} root={root} path={file.path} line={file.line} onBack={() => setFile(null)} />;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center gap-1.5 border-b p-2">
        <div className="relative flex-1">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              if (!e.target.value.trim()) setFound(null);
            }}
            placeholder="Find a file…"
            className="h-7 pl-7 text-xs"
          />
        </div>
        <button onClick={() => setGen((g) => g + 1)} className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground" title="Reload the tree">
          <RefreshCwIcon className="size-3.5" />
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-1">
        {pick && (
          <div className="mb-2 rounded-lg border border-amber-500/40 bg-amber-500/5 p-2 text-xs">
            <div className="mb-1 font-medium">Which one?</div>
            {pick.map((p) => (
              <button key={p} onClick={() => show(p)} className="block w-full truncate rounded px-1 py-0.5 text-left font-mono hover:bg-muted">
                {p}
              </button>
            ))}
          </div>
        )}
        {q.trim() && found ? (
          found.length === 0 ? (
            <div className="p-2 text-xs text-muted-foreground">No file matches.</div>
          ) : (
            found.map((p) => (
              <button key={p} onClick={() => show(p)} className="block w-full truncate rounded px-2 py-0.5 text-left font-mono text-xs hover:bg-muted" title={p}>
                {p}
              </button>
            ))
          )
        ) : (
          <Tree
            key={gen}
            root={root}
            dir=""
            depth={0}
            open={open}
            onPick={(e) => {
              if (e.dir) setOpen((o) => {
                const n = new Set(o);
                if (n.has(e.path)) n.delete(e.path);
                else n.add(e.path);
                return n;
              });
              else show(e.path);
            }}
          />
        )}
      </div>
    </div>
  );
}
