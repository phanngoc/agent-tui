"use client";

import * as React from "react";
import { XIcon, CircleIcon, GitBranchIcon, AlertTriangleIcon, ChevronRightIcon, FileIcon, LockIcon, PinIcon } from "lucide-react";
import { useRouter } from "next/navigation";
import { cn } from "@/lib/utils";
import { addFileToChat, copyText, fullPath } from "./actions";
import { editorService } from "./service";
import { useEditor, type Tab } from "./store";
import { ContextMenu, type MenuItem } from "@/components/context-menu";

/** closeMany closes tabs one by one, each asking first if it has unsaved changes. */
function closeMany(keys: string[]) {
  for (const k of keys) editorService.close(k);
}

// The editor's chrome: tabs, breadcrumbs, the status bar, and the dialog for
// a save that would overwrite someone else's change.

export function Tabs() {
  const tabs = useEditor((s) => s.tabs);
  const active = useEditor((s) => s.active);
  const setActive = useEditor((s) => s.setActive);
  const patchTab = useEditor((s) => s.patchTab);
  const bar = React.useRef<HTMLDivElement>(null);
  const [menu, setMenu] = React.useState<{ x: number; y: number; tab: Tab } | null>(null);
  const router = useRouter();

  // The right-click menu of a tab, as VS Code has it.
  const itemsFor = (t: Tab): MenuItem[] => {
    const st = useEditor.getState();
    const i = st.tabs.findIndex((x) => x.key === t.key);
    const others = st.tabs.filter((x) => x.key !== t.key && !x.pinned).map((x) => x.key);
    const right = st.tabs.slice(i + 1).filter((x) => !x.pinned).map((x) => x.key);
    const saved = st.tabs.filter((x) => !x.dirty && !x.pinned).map((x) => x.key);
    const all = st.tabs.filter((x) => !x.pinned).map((x) => x.key);
    const root = st.root;
    return [
      { label: "Close", hint: "Alt+W", run: () => editorService.close(t.key) },
      { label: "Close Others", run: () => closeMany(others), disabled: !others.length },
      { label: "Close to the Right", run: () => closeMany(right), disabled: !right.length },
      { label: "Close Saved", run: () => closeMany(saved), disabled: !saved.length },
      { label: "Close All", run: () => closeMany(all), disabled: !all.length },
      "-",
      { label: "Copy Path", run: () => void copyText(fullPath(root, t.path), "Path") },
      { label: "Copy Relative Path", run: () => void copyText(t.path, "Relative path") },
      "-",
      { label: "Add File to Chat", run: () => addFileToChat(root, t.path, router.push) },
      { label: "Reveal in Explorer View", run: () => st.revealFile(t.path) },
      t.kind === "file" ? { label: "Open Changes", run: () => void editorService.openDiff(t.path) } : { label: "Open File", run: () => void editorService.open(t.path) },
      "-",
      { label: "Keep Open", hint: "double-click", run: () => patchTab(t.key, { preview: false }), disabled: !t.preview },
      t.pinned ? { label: "Unpin", run: () => st.setPinned(t.key, false) } : { label: "Pin", run: () => st.setPinned(t.key, true) },
    ];
  };
  React.useEffect(() => {
    bar.current?.querySelector(`[data-key="${CSS.escape(active ?? "")}"]`)?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [active]);
  if (!tabs.length) return null;
  return (
    <div
      ref={bar}
      className="flex h-9 shrink-0 items-stretch overflow-x-auto border-b bg-muted/30 [&::-webkit-scrollbar]:h-1"
      onWheel={(e) => {
        if (e.deltaY) e.currentTarget.scrollLeft += e.deltaY;
      }}
    >
      {tabs.map((t) => {
        const name = t.path.split("/").pop();
        const on = t.key === active;
        return (
          <div
            key={t.key}
            data-key={t.key}
            onMouseDown={(e) => {
              if (e.button === 1) {
                e.preventDefault();
                editorService.close(t.key);
              }
            }}
            onClick={() => {
              setActive(t.key);
              editorService.show(t.key);
              editorService.editor?.focus();
            }}
            onDoubleClick={() => patchTab(t.key, { preview: false })}
            onContextMenu={(e) => {
              e.preventDefault();
              setMenu({ x: e.clientX, y: e.clientY, tab: t });
            }}
            title={t.path + (t.stale ? " — changed on disk" : "")}
            className={cn(
              "group flex shrink-0 cursor-pointer items-center gap-1.5 border-r px-3 text-[13px] select-none",
              on ? "bg-background text-foreground shadow-[inset_0_2px_0_var(--color-primary)]" : "text-muted-foreground hover:bg-background/60",
            )}
          >
            {t.pinned ? <PinIcon className="size-3 shrink-0 rotate-45 text-primary" /> : <FileIcon className="size-3.5 shrink-0" />}
            <span className={cn("max-w-48 truncate", t.preview && "italic")}>
              {name}
              {t.kind === "diff" && <span className="ml-1 text-[11px] opacity-70">(Working Tree)</span>}
            </span>
            {t.readOnly && <LockIcon className="size-3 opacity-60" />}
            {t.stale && <AlertTriangleIcon className="size-3 text-amber-500" />}
            <button
              onClick={(e) => {
                e.stopPropagation();
                if (t.pinned) useEditor.getState().setPinned(t.key, false);
                else editorService.close(t.key);
              }}
              className={cn("grid size-4 place-items-center rounded hover:bg-muted", t.pinned && "hidden group-hover:grid")}
              title={t.pinned ? "Unpin" : "Close (Alt+W)"}
            >
              {t.dirty ? (
                <>
                  <CircleIcon className="size-2 fill-current group-hover:hidden" />
                  <XIcon className="hidden size-3 group-hover:block" />
                </>
              ) : (
                <XIcon className={cn("size-3", !on && "opacity-0 group-hover:opacity-100")} />
              )}
            </button>
          </div>
        );
      })}
      {menu && <ContextMenu x={menu.x} y={menu.y} items={itemsFor(menu.tab)} onClose={() => setMenu(null)} />}
    </div>
  );
}

export function Breadcrumbs() {
  const active = useEditor((s) => s.active);
  if (!active) return null;
  const path = active.startsWith("diff:") ? active.slice(5) : active;
  const parts = path.split("/");
  return (
    <div className="flex h-6 shrink-0 items-center gap-0.5 overflow-hidden border-b px-3 text-[12px] text-muted-foreground">
      {parts.map((p, i) => (
        <React.Fragment key={i}>
          {i > 0 && <ChevronRightIcon className="size-3 shrink-0 opacity-60" />}
          <span className={cn("truncate", i === parts.length - 1 && "text-foreground")}>{p}</span>
        </React.Fragment>
      ))}
    </div>
  );
}

export function StatusBar({ onBranch }: { onBranch: () => void }) {
  const cursor = useEditor((s) => s.cursor);
  const lang = useEditor((s) => s.lang);
  const eol = useEditor((s) => s.eol);
  const indent = useEditor((s) => s.indent);
  const branch = useEditor((s) => s.branch);
  const active = useEditor((s) => s.active);
  const tab = useEditor((s) => s.tabs.find((t) => t.key === s.active));
  const setQuick = useEditor((s) => s.setQuick);
  return (
    <div className="flex h-6 shrink-0 items-center gap-3 border-t bg-primary px-3 text-[12px] text-primary-foreground">
      {branch && (
        <button onClick={onBranch} className="flex items-center gap-1 hover:opacity-80" title="Source Control">
          <GitBranchIcon className="size-3.5" />
          {branch}
        </button>
      )}
      <LspBadge />
      {tab?.stale && (
        <span className="flex items-center gap-1">
          <AlertTriangleIcon className="size-3.5" /> changed on disk — save to overwrite, or revert
        </span>
      )}
      <span className="flex-1" />
      {active && (
        <>
          <button onClick={() => setQuick({ mode: "line", text: ":" })} className="hover:opacity-80" title="Go to Line (Ctrl+G)">
            Ln {cursor.line}, Col {cursor.col}
            {cursor.selected ? ` (${cursor.selected} selected)` : ""}
          </button>
          <span>{indent}</span>
          <span>UTF-8</span>
          <span>{eol}</span>
          <span className="capitalize">{lang}</span>
          {tab?.readOnly && <span>Read-only</span>}
        </>
      )}
    </div>
  );
}

/** LspBadge says whether the project's language server is up. */
function LspBadge() {
  const lsp = useEditor((s) => s.lsp);
  if (lsp.state === "off") return null;
  const dot = lsp.state === "ready" ? "bg-emerald-400" : lsp.state === "error" ? "bg-red-400" : "animate-pulse bg-amber-300";
  const word = { ready: "", starting: "starting…", loading: "loading project…", error: "unavailable", off: "" }[lsp.state];
  return (
    <span className="flex items-center gap-1.5" title={lsp.message}>
      <span className={cn("size-2 rounded-full", dot)} />
      TS {word}
    </span>
  );
}

export function ConflictDialog() {
  const conflict = useEditor((s) => s.conflict);
  const setConflict = useEditor((s) => s.setConflict);
  if (!conflict) return null;
  const done = () => setConflict(null);
  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/30">
      <div className="w-[min(480px,92vw)] rounded-lg border bg-popover p-4 text-sm text-popover-foreground shadow-2xl">
        <div className="mb-1 flex items-center gap-2 font-medium">
          <AlertTriangleIcon className="size-4 text-amber-500" /> {conflict.path} changed on disk
        </div>
        <p className="mb-4 text-muted-foreground">
          Something else — the agent, a terminal, git — saved this file after you opened it. Saving now would overwrite that change.
        </p>
        <div className="flex flex-wrap justify-end gap-2">
          <button className="rounded-md border px-3 py-1.5 hover:bg-muted" onClick={done}>
            Cancel
          </button>
          <button
            className="rounded-md border px-3 py-1.5 hover:bg-muted"
            onClick={() => {
              done();
              void editorService.openDiff(conflict.path);
            }}
          >
            Compare with HEAD
          </button>
          <button
            className="rounded-md border px-3 py-1.5 hover:bg-muted"
            onClick={() => {
              done();
              void editorService.reload(conflict.path);
            }}
          >
            Discard mine, reload
          </button>
          <button
            className="rounded-md bg-primary px-3 py-1.5 text-primary-foreground hover:opacity-90"
            onClick={() => {
              done();
              void editorService.save(conflict.path, true);
            }}
          >
            Overwrite
          </button>
        </div>
      </div>
    </div>
  );
}
