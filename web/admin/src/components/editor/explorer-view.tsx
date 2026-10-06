"use client";

import * as React from "react";
import { ChevronRightIcon, FileIcon, FilePlusIcon, FolderIcon, FolderOpenIcon, FolderPlusIcon, RefreshCwIcon, ChevronsDownUpIcon } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { cn } from "@/lib/utils";
import { editorService } from "./service";
import { useEditor } from "./store";
import { invalidateFiles } from "./files";

// The explorer: the project as a tree, drawn only as far as it is on screen —
// a folder of thousands of files scrolls as fast as one of ten — with the
// file operations of VS Code's: new file and folder, rename, delete, copy
// path, by context menu or key.

type Entry = { name: string; path: string; dir: boolean };
type Row =
  | { kind: "entry"; e: Entry; depth: number }
  | { kind: "loading"; depth: number; key: string }
  | { kind: "edit"; depth: number; parent: string; mode: "file" | "dir" | "rename"; path?: string; name: string };
type Editing = { parent: string; mode: "file" | "dir" | "rename"; path?: string; name: string } | null;

const ROW = 22;

function parentOf(p: string) {
  const i = p.lastIndexOf("/");
  return i < 0 ? "" : p.slice(0, i);
}

export function ExplorerView() {
  const root = useEditor((s) => s.root);
  const active = useEditor((s) => s.active);
  const [children, setChildren] = React.useState<Map<string, Entry[]>>(new Map());
  const [expanded, setExpanded] = React.useState<Set<string>>(new Set());
  const [selected, setSelected] = React.useState<string>("");
  const [editing, setEditing] = React.useState<Editing>(null);
  const [menu, setMenu] = React.useState<{ x: number; y: number; e: Entry | null } | null>(null);
  const [scroll, setScroll] = React.useState(0);
  const [height, setHeight] = React.useState(600);
  const box = React.useRef<HTMLDivElement>(null);
  const loading = React.useRef<Set<string>>(new Set());

  const load = React.useCallback(
    async (dir: string, force = false) => {
      if (loading.current.has(dir)) return;
      if (!force && children.has(dir)) return;
      loading.current.add(dir);
      try {
        const r = await api.get<{ entries: Entry[] }>("/api/files/list" + qs({ root, dir }));
        setChildren((m) => new Map(m).set(dir, r.entries));
      } catch (e) {
        toast.error((e as Error).message);
      } finally {
        loading.current.delete(dir);
      }
    },
    [root, children],
  );

  // The project's top level, once. (A new project gets a new explorer: the
  // workbench keys it by root.)
  React.useEffect(() => {
    if (!root) return;
    let live = true;
    api
      .get<{ entries: Entry[] }>("/api/files/list" + qs({ root, dir: "" }))
      .then((r) => live && setChildren((m) => new Map(m).set("", r.entries)))
      .catch((e) => toast.error((e as Error).message));
    return () => {
      live = false;
    };
  }, [root]);

  // Reveal the file being edited when it changes: its folders open, it
  // selected and in view. A store subscription, so it runs on the change
  // itself rather than after every render.
  const loadRef = React.useRef(load);
  React.useEffect(() => {
    loadRef.current = load;
  }, [load]);
  React.useEffect(() => {
    const reveal = (a: string | null) => {
      if (!a || a.startsWith("diff:")) return;
      const parts = a.split("/");
      const dirs = parts.slice(0, -1).map((_, i) => parts.slice(0, i + 1).join("/"));
      setExpanded((x) => (dirs.every((d) => x.has(d)) ? x : new Set([...x, ...dirs])));
      setSelected(a);
      dirs.forEach((d) => void loadRef.current(d));
    };
    return useEditor.subscribe((st, prev) => {
      if (st.active !== prev.active) reveal(st.active);
    });
  }, []);

  const rows = React.useMemo(() => {
    const out: Row[] = [];
    const walk = (dir: string, depth: number) => {
      if (editing && editing.mode !== "rename" && editing.parent === dir) out.push({ kind: "edit", depth, parent: dir, mode: editing.mode, name: editing.name });
      const list = children.get(dir);
      if (!list) {
        out.push({ kind: "loading", depth, key: "loading:" + dir });
        return;
      }
      for (const e of list) {
        if (editing?.mode === "rename" && editing.path === e.path) out.push({ kind: "edit", depth, parent: dir, mode: "rename", path: e.path, name: editing.name });
        else out.push({ kind: "entry", e, depth });
        if (e.dir && expanded.has(e.path)) walk(e.path, depth + 1);
      }
    };
    if (root) walk("", 0);
    return out;
  }, [children, expanded, editing, root]);

  React.useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setHeight(el.clientHeight));
    ro.observe(el);
    setHeight(el.clientHeight);
    return () => ro.disconnect();
  }, []);

  // Keep the selected row in view when it moves by key or reveal.
  React.useEffect(() => {
    const i = rows.findIndex((r) => r.kind === "entry" && r.e.path === selected);
    const el = box.current;
    if (i < 0 || !el) return;
    const top = i * ROW;
    if (top < el.scrollTop) el.scrollTop = top;
    else if (top + ROW > el.scrollTop + el.clientHeight) el.scrollTop = top + ROW - el.clientHeight;
  }, [selected, rows]);

  const toggle = (e: Entry) => {
    setExpanded((x) => {
      const n = new Set(x);
      if (n.has(e.path)) n.delete(e.path);
      else {
        n.add(e.path);
        void load(e.path);
      }
      return n;
    });
  };

  const refresh = async (dirs?: string[]) => {
    const list = dirs ?? ["", ...expanded];
    await Promise.all(list.map((d) => load(d, true)));
  };

  const commit = async (ed: NonNullable<Editing>, name: string) => {
    setEditing(null);
    name = name.trim();
    if (!name) return;
    const target = ed.parent ? `${ed.parent}/${name}` : name;
    try {
      if (ed.mode === "rename" && ed.path) {
        if (target === ed.path) return;
        await api.post("/api/files/rename", { root, from: ed.path, to: target });
        editorService.renamed(ed.path, target);
        await refresh([ed.parent, parentOf(target)]);
      } else {
        await api.post("/api/files/create", { root, path: target, dir: ed.mode === "dir" });
        await refresh([ed.parent]);
        if (ed.mode === "file") void editorService.open(target);
        else setExpanded((x) => new Set([...x, target]));
      }
      setSelected(target);
      invalidateFiles();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const remove = async (e: Entry) => {
    if (!confirm(`Delete ${e.path}${e.dir ? " and everything in it" : ""}? This cannot be undone.`)) return;
    try {
      await api.post("/api/files/delete", { root, path: e.path });
      editorService.deleted(e.path);
      await refresh([parentOf(e.path)]);
      invalidateFiles();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const newIn = (e: Entry | null, mode: "file" | "dir") => {
    const parent = e ? (e.dir ? e.path : parentOf(e.path)) : selected ? (rows.find((r) => r.kind === "entry" && r.e.path === selected && r.e.dir) ? selected : parentOf(selected)) : "";
    if (parent) {
      setExpanded((x) => new Set([...x, parent]));
      void load(parent);
    }
    setEditing({ parent, mode, name: "" });
  };

  const onKey = (ev: React.KeyboardEvent) => {
    if (editing) return;
    const i = rows.findIndex((r) => r.kind === "entry" && r.e.path === selected);
    const cur = i >= 0 ? (rows[i] as Extract<Row, { kind: "entry" }>) : null;
    const move = (d: number) => {
      for (let j = i + d; j >= 0 && j < rows.length; j += d) {
        const r = rows[j];
        if (r.kind === "entry") return setSelected(r.e.path);
      }
    };
    switch (ev.key) {
      case "ArrowDown":
        ev.preventDefault();
        move(1);
        break;
      case "ArrowUp":
        ev.preventDefault();
        move(-1);
        break;
      case "ArrowRight":
        if (cur?.e.dir && !expanded.has(cur.e.path)) toggle(cur.e);
        break;
      case "ArrowLeft":
        if (cur?.e.dir && expanded.has(cur.e.path)) toggle(cur.e);
        else if (cur) setSelected(parentOf(cur.e.path));
        break;
      case "Enter":
        if (cur?.e.dir) toggle(cur.e);
        else if (cur) void editorService.open(cur.e.path);
        break;
      case "F2":
        if (cur) setEditing({ parent: parentOf(cur.e.path), mode: "rename", path: cur.e.path, name: cur.e.name });
        break;
      case "Delete":
        if (cur) void remove(cur.e);
        break;
    }
  };

  const start = Math.max(0, Math.floor(scroll / ROW) - 10);
  const end = Math.min(rows.length, Math.ceil((scroll + height) / ROW) + 10);

  return (
    <div className="flex min-h-0 flex-1 flex-col" onClick={() => setMenu(null)}>
      <div className="flex items-center gap-0.5 px-3 py-1.5 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
        <span className="min-w-0 flex-1 truncate" title={root}>
          {root.split(/[\\/]/).filter(Boolean).pop()}
        </span>
        <IconBtn title="New file" onClick={() => newIn(null, "file")}>
          <FilePlusIcon />
        </IconBtn>
        <IconBtn title="New folder" onClick={() => newIn(null, "dir")}>
          <FolderPlusIcon />
        </IconBtn>
        <IconBtn title="Refresh" onClick={() => void refresh()}>
          <RefreshCwIcon />
        </IconBtn>
        <IconBtn title="Collapse all" onClick={() => setExpanded(new Set())}>
          <ChevronsDownUpIcon />
        </IconBtn>
      </div>
      <div
        ref={box}
        tabIndex={0}
        onKeyDown={onKey}
        onScroll={(e) => setScroll(e.currentTarget.scrollTop)}
        onContextMenu={(e) => {
          e.preventDefault();
          setMenu({ x: e.clientX, y: e.clientY, e: null });
        }}
        className="relative min-h-0 flex-1 overflow-auto outline-none focus-visible:ring-1 focus-visible:ring-ring/40"
      >
        <div style={{ height: rows.length * ROW }} className="relative">
          {rows.slice(start, end).map((r, k) => {
            const top = (start + k) * ROW;
            const pad = r.depth * 12 + 8;
            if (r.kind === "loading")
              return (
                <div key={r.key} className="absolute right-0 left-0 text-xs text-muted-foreground" style={{ top, height: ROW, paddingLeft: pad + 18, lineHeight: `${ROW}px` }}>
                  …
                </div>
              );
            if (r.kind === "edit")
              return (
                <div key={"edit:" + (r.path ?? r.parent)} className="absolute right-0 left-0 flex items-center gap-1 pr-2" style={{ top, height: ROW, paddingLeft: pad }}>
                  {r.mode === "dir" ? <FolderIcon className="size-3.5 shrink-0 text-sky-600" /> : <FileIcon className="size-3.5 shrink-0 text-muted-foreground" />}
                  <input
                    autoFocus
                    defaultValue={r.name}
                    onFocus={(e) => {
                      const v = e.currentTarget.value;
                      const dot = v.lastIndexOf(".");
                      e.currentTarget.setSelectionRange(0, dot > 0 ? dot : v.length);
                    }}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") void commit(editing!, e.currentTarget.value);
                      if (e.key === "Escape") setEditing(null);
                      e.stopPropagation();
                    }}
                    onBlur={(e) => void commit(editing!, e.currentTarget.value)}
                    className="h-5 min-w-0 flex-1 rounded-sm border border-primary bg-background px-1 font-mono text-[12px] outline-none"
                  />
                </div>
              );
            const e = r.e;
            return (
              <button
                key={e.path}
                onClick={(ev) => {
                  ev.stopPropagation();
                  setSelected(e.path);
                  box.current?.focus();
                  if (e.dir) toggle(e);
                  else void editorService.open(e.path, { preview: true, focus: false });
                }}
                onDoubleClick={() => !e.dir && void editorService.open(e.path, { preview: false })}
                onContextMenu={(ev) => {
                  ev.preventDefault();
                  ev.stopPropagation();
                  setSelected(e.path);
                  setMenu({ x: ev.clientX, y: ev.clientY, e });
                }}
                title={e.path}
                className={cn(
                  "absolute right-0 left-0 flex items-center gap-1 pr-2 text-left text-[13px] hover:bg-muted/60",
                  selected === e.path && "bg-primary/10",
                  active === e.path && "text-primary",
                )}
                style={{ top, height: ROW, paddingLeft: pad }}
              >
                {e.dir ? (
                  <>
                    <ChevronRightIcon className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", expanded.has(e.path) && "rotate-90")} />
                    {expanded.has(e.path) ? <FolderOpenIcon className="size-3.5 shrink-0 text-sky-600" /> : <FolderIcon className="size-3.5 shrink-0 text-sky-600" />}
                  </>
                ) : (
                  <>
                    <span className="w-3.5 shrink-0" />
                    <FileIcon className="size-3.5 shrink-0 text-muted-foreground" />
                  </>
                )}
                <span className="truncate">{e.name}</span>
              </button>
            );
          })}
        </div>
      </div>
      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          items={[
            { label: "New File…", run: () => newIn(menu.e, "file") },
            { label: "New Folder…", run: () => newIn(menu.e, "dir") },
            ...(menu.e
              ? [
                  ...(!menu.e.dir ? [{ label: "Open", run: () => void editorService.open(menu.e!.path) }, { label: "Open Changes (git)", run: () => void editorService.openDiff(menu.e!.path) }] : []),
                  { label: "Rename…  F2", run: () => setEditing({ parent: parentOf(menu.e!.path), mode: "rename", path: menu.e!.path, name: menu.e!.name }) },
                  { label: "Delete  Del", run: () => void remove(menu.e!), danger: true },
                  { label: "Copy Relative Path", run: () => void navigator.clipboard.writeText(menu.e!.path) },
                ]
              : [{ label: "Refresh", run: () => void refresh() }]),
          ]}
          onClose={() => setMenu(null)}
        />
      )}
    </div>
  );
}

export function IconBtn({ title, onClick, children, active }: { title: string; onClick: () => void; children: React.ReactNode; active?: boolean }) {
  return (
    <button
      title={title}
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      className={cn("rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground [&_svg]:size-3.5", active && "bg-muted text-foreground")}
    >
      {children}
    </button>
  );
}

export function ContextMenu({ x, y, items, onClose }: { x: number; y: number; items: { label: string; run: () => void; danger?: boolean }[]; onClose: () => void }) {
  const ref = React.useRef<HTMLDivElement>(null);
  React.useEffect(() => {
    const close = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) onClose();
    };
    const esc = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", esc);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", esc);
    };
  }, [onClose]);
  return (
    <div
      ref={ref}
      className="fixed z-50 min-w-48 rounded-md border bg-popover py-1 text-[13px] text-popover-foreground shadow-lg"
      style={{ left: Math.min(x, window.innerWidth - 220), top: Math.min(y, window.innerHeight - items.length * 28 - 16) }}
    >
      {items.map((it) => (
        <button
          key={it.label}
          onClick={(e) => {
            e.stopPropagation();
            onClose();
            it.run();
          }}
          className={cn("block w-full px-3 py-1 text-left hover:bg-primary hover:text-primary-foreground", it.danger && "text-destructive")}
        >
          {it.label}
        </button>
      ))}
    </div>
  );
}
