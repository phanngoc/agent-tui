"use client";

import * as React from "react";
import { CaseSensitiveIcon, RegexIcon, ChevronRightIcon, FileIcon } from "lucide-react";
import { api, qs } from "@/lib/api";
import { cn } from "@/lib/utils";
import { editorService } from "./service";
import { useEditor } from "./store";
import { IconBtn } from "./explorer-view";

// Search across the project, as VS Code's search view: case and regex
// toggles, an include filter, results grouped by file, each opening at its
// match. The scan runs in the gateway (in the distribution, for a WSL
// project); the page only draws what came back, a group at a time.

type Hit = { Path: string; Line: number; Text: string; Start: number; End: number };

export const searchFocus = { current: null as HTMLInputElement | null };

export function SearchView() {
  const root = useEditor((s) => s.root);
  const [q, setQ] = React.useState("");
  const [cs, setCs] = React.useState(false);
  const [re, setRe] = React.useState(false);
  const [include, setInclude] = React.useState("");
  const [hits, setHits] = React.useState<Hit[] | null>(null);
  const [err, setErr] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [truncated, setTruncated] = React.useState(false);
  const [folded, setFolded] = React.useState<Set<string>>(new Set());
  const seq = React.useRef(0);
  const input = React.useRef<HTMLInputElement>(null);
  React.useEffect(() => {
    searchFocus.current = input.current;
    input.current?.focus();
    return () => {
      searchFocus.current = null;
    };
  }, []);

  React.useEffect(() => {
    if (!q.trim()) return;
    const mine = ++seq.current;
    const t = setTimeout(async () => {
      setBusy(true);
      setErr(null);
      try {
        const r = await api.get<{ hits: Hit[]; truncated: boolean }>("/api/files/search" + qs({ root, q, case: cs ? "1" : "", regex: re ? "1" : "", include }));
        if (mine !== seq.current) return;
        setHits(r.hits);
        setTruncated(r.truncated);
      } catch (e) {
        if (mine === seq.current) setErr((e as Error).message);
      } finally {
        if (mine === seq.current) setBusy(false);
      }
    }, 250);
    return () => clearTimeout(t);
  }, [q, cs, re, include, root]);

  const groups = React.useMemo(() => {
    const m = new Map<string, Hit[]>();
    for (const h of hits ?? []) {
      const g = m.get(h.Path);
      if (g) g.push(h);
      else m.set(h.Path, [h]);
    }
    return [...m.entries()];
  }, [hits]);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="px-3 py-1.5 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">Search</div>
      <div className="space-y-1.5 px-2 pb-2">
        <div className="flex items-center gap-0.5 rounded border bg-background pr-1 focus-within:ring-1 focus-within:ring-ring">
          <input
            ref={input}
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              if (!e.target.value.trim()) setHits(null);
            }}
            placeholder="Search"
            className="min-w-0 flex-1 bg-transparent px-2 py-1 text-[13px] outline-none"
          />
          <IconBtn title="Match Case" onClick={() => setCs(!cs)} active={cs}>
            <CaseSensitiveIcon />
          </IconBtn>
          <IconBtn title="Use Regular Expression" onClick={() => setRe(!re)} active={re}>
            <RegexIcon />
          </IconBtn>
        </div>
        <input
          value={include}
          onChange={(e) => setInclude(e.target.value)}
          placeholder="files to include, e.g. *.go, web/**"
          className="w-full rounded border bg-background px-2 py-1 text-[12px] outline-none focus:ring-1 focus:ring-ring"
        />
        <div className="text-[11px] text-muted-foreground">
          {busy ? "Searching…" : err ? <span className="text-destructive">{err}</span> : hits ? `${hits.length}${truncated ? "+" : ""} results in ${groups.length} files` : ""}
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-auto pb-2 text-[13px]">
        {groups.map(([path, list]) => (
          <div key={path}>
            <button
              onClick={() => setFolded((f) => {
                const n = new Set(f);
                if (n.has(path)) n.delete(path);
                else n.add(path);
                return n;
              })}
              className="flex w-full min-w-0 items-center gap-1 px-2 py-0.5 text-left hover:bg-muted/60"
              title={path}
            >
              <ChevronRightIcon className={cn("size-3.5 shrink-0 transition-transform", !folded.has(path) && "rotate-90")} />
              <FileIcon className="size-3.5 shrink-0 text-muted-foreground" />
              <span className="truncate">{path.split("/").pop()}</span>
              <span className="truncate text-[11px] text-muted-foreground">{path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ""}</span>
              <span className="ml-auto shrink-0 rounded-full bg-muted px-1.5 text-[10px]">{list.length}</span>
            </button>
            {!folded.has(path) &&
              list.slice(0, 200).map((h, i) => {
                const text = h.Text;
                const lead = Math.max(0, h.Start - 30);
                return (
                  <button
                    key={i}
                    onClick={() => void editorService.open(path, { line: h.Line, col: h.Start + 1, endCol: h.End + 1, preview: true })}
                    className="block w-full truncate py-0.5 pr-2 pl-9 text-left font-mono text-[12px] hover:bg-muted/60"
                    title={`${path}:${h.Line}`}
                  >
                    {lead > 0 && "…"}
                    {text.slice(lead, h.Start)}
                    <mark className="rounded-sm bg-amber-400/40 text-inherit">{text.slice(h.Start, h.End)}</mark>
                    {text.slice(h.End, h.End + 200)}
                  </button>
                );
              })}
          </div>
        ))}
      </div>
    </div>
  );
}
