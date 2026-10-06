"use client";

import * as React from "react";
import { FileIcon, TerminalIcon, HashIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Match } from "@/lib/fuzzy";
import { editorService } from "./service";
import { useEditor } from "./store";
import { fileIndex } from "./files";

// Quick open and the command palette, one box as in VS Code: a file by fuzzy
// name; ">" a command; ":" a line. Matching runs in the page over the cached
// file list, so each keystroke answers at once, however large the project.

export type Command = { id: string; label: string; keys?: string; run: () => void };

function Highlight({ text, hits }: { text: string; hits: number[] }) {
  if (!hits.length) return <>{text}</>;
  const set = new Set(hits);
  const out: React.ReactNode[] = [];
  let buf = "";
  let on = false;
  for (let i = 0; i <= text.length; i++) {
    const h = set.has(i);
    if (i === text.length || h !== on) {
      if (buf) out.push(on ? <b key={i} className="text-sky-600 dark:text-sky-400">{buf}</b> : <span key={i}>{buf}</span>);
      buf = "";
      on = h;
    }
    if (i < text.length) buf += text[i];
  }
  return <>{out}</>;
}

function scoreLabel(label: string, q: string): number {
  const l = label.toLowerCase();
  if (!q) return 1;
  if (l.includes(q)) return 100 - l.indexOf(q);
  let qi = 0;
  for (let i = 0; i < l.length && qi < q.length; i++) if (l[i] === q[qi]) qi++;
  return qi === q.length ? 10 : -1;
}

export function QuickOpen({ commands }: { commands: Command[] }) {
  const quick = useEditor((s) => s.quick);
  const setQuick = useEditor((s) => s.setQuick);
  const root = useEditor((s) => s.root);
  const tabs = useEditor((s) => s.tabs);
  const [text, setText] = React.useState(quick?.text ?? "");
  const [files, setFiles] = React.useState<Match[]>([]);
  const [count, setCount] = React.useState(0);
  const [sel, setSel] = React.useState(0);
  const list = React.useRef<HTMLDivElement>(null);
  const mode = text.startsWith(">") ? "commands" : text.startsWith(":") ? "line" : "files";
  const query = mode === "files" ? text : text.slice(1).trim();

  React.useEffect(() => {
    if (mode !== "files" || !root) return;
    let live = true;
    void fileIndex(root).then((ix) => {
      if (!live) return;
      setCount(ix.size);
      if (!query.trim()) {
        // Nothing typed: the open files, most recent first, then the rest.
        const open = tabs.filter((t) => t.kind === "file").map((t) => t.path);
        setFiles([...open.map((path) => ({ path, score: 0, hits: [] })), ...ix.search("", 40).filter((m) => !open.includes(m.path))].slice(0, 60));
      } else setFiles(ix.search(query, 60));
      setSel(0);
    });
    return () => {
      live = false;
    };
  }, [mode, query, root, tabs]);

  const cmds = React.useMemo(() => {
    if (mode !== "commands") return [];
    const q = query.toLowerCase();
    return commands
      .map((c) => ({ c, s: scoreLabel(c.label, q) }))
      .filter((x) => x.s >= 0)
      .sort((a, b) => b.s - a.s)
      .map((x) => x.c);
  }, [mode, query, commands]);

  const items = mode === "files" ? files.length : mode === "commands" ? cmds.length : 1;
  const close = () => setQuick(null);
  const choose = (i: number) => {
    if (mode === "files") {
      const m = files[i];
      if (!m) return;
      close();
      void editorService.open(m.path);
    } else if (mode === "commands") {
      const c = cmds[i];
      if (!c) return;
      close();
      setTimeout(c.run, 0);
    } else {
      const [l, c] = query.split(/[:,]/).map((x) => parseInt(x, 10));
      const p = editorService.activePath();
      close();
      if (p && l > 0) void editorService.open(p, { line: l, col: c > 0 ? c : 1 });
    }
  };

  React.useEffect(() => {
    list.current?.querySelector(`[data-i="${sel}"]`)?.scrollIntoView({ block: "nearest" });
  }, [sel]);

  if (!quick) return null;
  return (
    <div className="fixed inset-0 z-50" onMouseDown={close}>
      <div className="mx-auto mt-[8vh] w-[min(640px,92vw)] overflow-hidden rounded-lg border bg-popover text-popover-foreground shadow-2xl" onMouseDown={(e) => e.stopPropagation()}>
        <input
          autoFocus
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setSel(0);
          }}
          onKeyDown={(e) => {
            if (e.key === "Escape") close();
            else if (e.key === "ArrowDown") {
              e.preventDefault();
              setSel((s) => Math.min(items - 1, s + 1));
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              setSel((s) => Math.max(0, s - 1));
            } else if (e.key === "Enter") {
              e.preventDefault();
              choose(sel);
            }
            e.stopPropagation();
          }}
          placeholder={mode === "files" ? `Search files by name (${count.toLocaleString()} files) — ">" for commands, ":" for a line` : ""}
          className="w-full border-b bg-transparent px-3 py-2.5 text-[14px] outline-none"
        />
        <div ref={list} className="max-h-[50vh] overflow-auto py-1">
          {mode === "line" && (
            <div className="flex items-center gap-2 bg-primary/10 px-3 py-1.5 text-[13px]">
              <HashIcon className="size-3.5" />
              {query ? `Go to line ${query}` : "Type a line number, optionally :column"}
            </div>
          )}
          {mode === "files" &&
            files.map((m, i) => {
              const slash = m.path.lastIndexOf("/");
              const name = m.path.slice(slash + 1);
              const dir = slash >= 0 ? m.path.slice(0, slash) : "";
              return (
                <button
                  key={m.path}
                  data-i={i}
                  onMouseMove={() => setSel(i)}
                  onClick={() => choose(i)}
                  className={cn("flex w-full min-w-0 items-center gap-2 px-3 py-1 text-left text-[13px]", sel === i && "bg-primary/15")}
                >
                  <FileIcon className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="shrink-0">
                    <Highlight text={name} hits={m.hits.filter((h) => h > slash).map((h) => h - slash - 1)} />
                  </span>
                  <span className="truncate text-[12px] text-muted-foreground">
                    <Highlight text={dir} hits={m.hits.filter((h) => h < slash)} />
                  </span>
                </button>
              );
            })}
          {mode === "commands" &&
            cmds.map((c, i) => (
              <button
                key={c.id}
                data-i={i}
                onMouseMove={() => setSel(i)}
                onClick={() => choose(i)}
                className={cn("flex w-full items-center gap-2 px-3 py-1 text-left text-[13px]", sel === i && "bg-primary/15")}
              >
                <TerminalIcon className="size-3.5 shrink-0 text-muted-foreground" />
                <span className="flex-1">{c.label}</span>
                {c.keys && <kbd className="rounded border bg-muted px-1.5 font-mono text-[11px] text-muted-foreground">{c.keys}</kbd>}
              </button>
            ))}
          {mode !== "line" && items === 0 && <div className="px-3 py-2 text-[13px] text-muted-foreground">No matches</div>}
        </div>
      </div>
    </div>
  );
}
