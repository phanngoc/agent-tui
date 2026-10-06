"use client";

import * as React from "react";
import { FilesIcon, SearchIcon, GitBranchIcon, SquareTerminalIcon, LoaderCircleIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { TerminalPanel } from "@/components/terminal-panel";
import { editorService } from "./service";
import { useEditor, type View } from "./store";
import { ExplorerView } from "./explorer-view";
import { SearchView, searchFocus } from "./search-view";
import { ScmView } from "./scm-view";
import { QuickOpen, type Command } from "./quick-open";
import { Breadcrumbs, ConflictDialog, StatusBar, Tabs } from "./chrome";

// The workbench: VS Code's layout — activity bar, side bar, editor with tabs,
// panel, status bar — around one Monaco editor.

function useStored(key: string, init: number): [number, (n: number) => void] {
  const [v, setV] = React.useState(() => {
    try {
      return Number(localStorage.getItem(key)) || init;
    } catch {
      return init;
    }
  });
  const set = React.useCallback(
    (n: number) => {
      setV(n);
      try {
        localStorage.setItem(key, String(n));
      } catch {}
    },
    [key],
  );
  return [v, set];
}

/** drag resizes along one axis from a pointer-down, calling back with the new size. */
function dragResize(e: React.PointerEvent, start: number, axis: "x" | "y", dir: 1 | -1, clamp: (n: number) => number, set: (n: number) => void) {
  e.preventDefault();
  const p0 = axis === "x" ? e.clientX : e.clientY;
  let last = start;
  const move = (ev: PointerEvent) => {
    last = clamp(start + dir * ((axis === "x" ? ev.clientX : ev.clientY) - p0));
    set(last);
  };
  const up = () => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", up);
    document.body.style.cursor = "";
    set(last);
  };
  document.body.style.cursor = axis === "x" ? "col-resize" : "row-resize";
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
}

function isDark() {
  return document.documentElement.classList.contains("dark");
}

export function Workbench() {
  const root = useEditor((s) => s.root);
  const view = useEditor((s) => s.view);
  const sidebar = useEditor((s) => s.sidebar);
  const panel = useEditor((s) => s.panel);
  const active = useEditor((s) => s.active);
  const hasTabs = useEditor((s) => s.tabs.length > 0);
  const setView = useEditor((s) => s.setView);
  const toggleSidebar = useEditor((s) => s.toggleSidebar);
  const togglePanel = useEditor((s) => s.togglePanel);
  const setQuick = useEditor((s) => s.setQuick);
  const [sideW, setSideW] = useStored("agent-tui.editor.side", 280);
  const [panelH, setPanelH] = useStored("agent-tui.editor.panel", 260);
  const codeEl = React.useRef<HTMLDivElement>(null);
  const diffEl = React.useRef<HTMLDivElement>(null);
  const [ready, setReady] = React.useState(false);
  const [loadErr, setLoadErr] = React.useState<string | null>(null);

  // One Monaco for the page's life.
  React.useEffect(() => {
    if (!codeEl.current || !diffEl.current) return;
    let live = true;
    editorService
      .attach(codeEl.current, diffEl.current, isDark())
      .then(() => live && setReady(true))
      .catch((e) => live && setLoadErr((e as Error).message));
    const mo = new MutationObserver(() => editorService.setTheme(isDark()));
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => {
      live = false;
      mo.disconnect();
      editorService.editor?.dispose();
      editorService.diff?.dispose();
      editorService.editor = null;
      editorService.diff = null;
      editorService.reset();
    };
  }, []);

  React.useEffect(() => {
    if (active && ready) editorService.show(active);
  }, [active, ready]);

  // Files changed elsewhere — by the agent, a terminal, git — are noticed.
  React.useEffect(() => {
    const check = () => document.visibilityState === "visible" && void editorService.checkDisk();
    const t = setInterval(check, 4000);
    window.addEventListener("focus", check);
    return () => {
      clearInterval(t);
      window.removeEventListener("focus", check);
    };
  }, []);

  // Unsaved work is not lost to a closed tab.
  React.useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => {
      if (editorService.anyDirty()) {
        e.preventDefault();
        e.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, []);

  const show = React.useCallback(
    (v: View) => {
      const st = useEditor.getState();
      if (st.view === v && st.sidebar) toggleSidebar(false);
      else setView(v);
      if (v === "search") setTimeout(() => searchFocus.current?.focus(), 0);
    },
    [setView, toggleSidebar],
  );

  const wordWrap = React.useRef(false);
  const minimap = React.useRef(true);
  const commands: Command[] = React.useMemo(
    () => [
      { id: "files", label: "Go to File…", keys: "Ctrl+P", run: () => setQuick({ mode: "files", text: "" }) },
      { id: "save", label: "File: Save", keys: "Ctrl+S", run: () => void editorService.saveActive() },
      { id: "saveAll", label: "File: Save All", keys: "Ctrl+Alt+S", run: () => void editorService.saveAll() },
      { id: "revert", label: "File: Revert File", run: () => { const p = editorService.activePath(); if (p) void editorService.reload(p); } },
      { id: "close", label: "View: Close Editor", keys: "Alt+W", run: () => { const a = useEditor.getState().active; if (a) editorService.close(a); } },
      { id: "closeAll", label: "View: Close All Editors", run: () => useEditor.getState().tabs.slice().forEach((t) => editorService.close(t.key)) },
      { id: "line", label: "Go to Line…", keys: "Ctrl+G", run: () => setQuick({ mode: "line", text: ":" }) },
      { id: "find", label: "Find", keys: "Ctrl+F", run: () => editorService.run("actions.find") },
      { id: "replace", label: "Replace", keys: "Ctrl+H", run: () => editorService.run("editor.action.startFindReplaceAction") },
      { id: "format", label: "Format Document", keys: "Shift+Alt+F", run: () => editorService.run("editor.action.formatDocument") },
      { id: "symbol", label: "Go to Symbol in Editor…", keys: "Ctrl+Shift+O", run: () => editorService.run("editor.action.quickOutline") },
      { id: "def", label: "Go to Definition", keys: "F12", run: () => editorService.run("editor.action.revealDefinition") },
      { id: "rename", label: "Rename Symbol", keys: "F2", run: () => editorService.run("editor.action.rename") },
      { id: "comment", label: "Toggle Line Comment", keys: "Ctrl+/", run: () => editorService.run("editor.action.commentLine") },
      { id: "fold", label: "Fold All", run: () => editorService.run("editor.foldAll") },
      { id: "unfold", label: "Unfold All", run: () => editorService.run("editor.unfoldAll") },
      { id: "wrap", label: "View: Toggle Word Wrap", keys: "Alt+Z", run: () => { wordWrap.current = !wordWrap.current; editorService.editor?.updateOptions({ wordWrap: wordWrap.current ? "on" : "off" }); } },
      { id: "minimap", label: "View: Toggle Minimap", run: () => { minimap.current = !minimap.current; editorService.editor?.updateOptions({ minimap: { enabled: minimap.current } }); } },
      { id: "sidebar", label: "View: Toggle Side Bar", keys: "Ctrl+B", run: () => toggleSidebar() },
      { id: "explorer", label: "View: Show Explorer", keys: "Ctrl+Shift+E", run: () => setView("explorer") },
      { id: "search", label: "View: Show Search", keys: "Ctrl+Shift+F", run: () => show("search") },
      { id: "scm", label: "View: Show Source Control", keys: "Ctrl+Shift+G", run: () => setView("scm") },
      { id: "terminal", label: "View: Toggle Terminal", keys: "Ctrl+`", run: () => togglePanel() },
      { id: "diff", label: "Git: Open Changes", run: () => { const p = editorService.activePath(); if (p) void editorService.openDiff(p); } },
      { id: "monaco", label: "Editor: All Editor Commands…", keys: "F1", run: () => editorService.run("editor.action.quickCommand") },
      { id: "zoomIn", label: "Editor: Increase Font Size", run: () => editorService.run("editor.action.fontZoomIn") },
      { id: "zoomOut", label: "Editor: Decrease Font Size", run: () => editorService.run("editor.action.fontZoomOut") },
    ],
    [setQuick, toggleSidebar, togglePanel, setView, show],
  );

  // VS Code's keys. Caught at the window before anything else — the app
  // shell, Monaco, the browser's own Ctrl+P — and only the ones handled here.
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const ctrl = e.ctrlKey || e.metaKey;
      const k = e.key.toLowerCase();
      let run: (() => void) | null = null;
      if (ctrl && e.shiftKey && k === "p") run = () => setQuick({ mode: "commands", text: ">" });
      else if (ctrl && !e.shiftKey && !e.altKey && k === "p") run = () => setQuick({ mode: "files", text: "" });
      else if (ctrl && e.altKey && k === "s") run = () => void editorService.saveAll();
      else if (ctrl && !e.shiftKey && k === "s") run = () => void editorService.saveActive();
      else if (ctrl && !e.shiftKey && k === "b") run = () => toggleSidebar();
      else if (ctrl && e.shiftKey && k === "e") run = () => setView("explorer");
      else if (ctrl && e.shiftKey && k === "f") run = () => show("search");
      else if (ctrl && e.shiftKey && k === "g") run = () => setView("scm");
      else if (ctrl && (e.key === "`" || e.code === "Backquote")) run = () => togglePanel();
      else if (ctrl && !e.shiftKey && k === "g") run = () => setQuick({ mode: "line", text: ":" });
      else if (e.altKey && !ctrl && k === "w") run = () => { const a = useEditor.getState().active; if (a) editorService.close(a); };
      else if (e.altKey && !ctrl && /^[1-9]$/.test(e.key)) run = () => {
        const t = useEditor.getState().tabs[Number(e.key) - 1];
        if (t) useEditor.getState().setActive(t.key);
      };
      if (!run) return;
      e.preventDefault();
      e.stopPropagation();
      run();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [setQuick, toggleSidebar, setView, togglePanel, show]);

  const activities: [View, React.ComponentType<{ className?: string }>, string][] = [
    ["explorer", FilesIcon, "Explorer (Ctrl+Shift+E)"],
    ["search", SearchIcon, "Search (Ctrl+Shift+F)"],
    ["scm", GitBranchIcon, "Source Control (Ctrl+Shift+G)"],
  ];

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex min-h-0 flex-1">
        {/* Activity bar */}
        <div className="flex w-12 shrink-0 flex-col items-center gap-1 border-r bg-muted/40 py-2">
          {activities.map(([v, Icon, title]) => (
            <button
              key={v}
              title={title}
              onClick={() => show(v)}
              className={cn(
                "relative grid size-10 place-items-center rounded-md text-muted-foreground hover:text-foreground",
                sidebar && view === v && "text-foreground before:absolute before:top-1.5 before:bottom-1.5 before:left-[-4px] before:w-0.5 before:rounded before:bg-primary",
              )}
            >
              <Icon className="size-5" />
            </button>
          ))}
          <div className="flex-1" />
          <button title="Terminal (Ctrl+`)" onClick={() => togglePanel()} className={cn("grid size-10 place-items-center rounded-md text-muted-foreground hover:text-foreground", panel && "text-foreground")}>
            <SquareTerminalIcon className="size-5" />
          </button>
        </div>

        {/* Side bar */}
        {sidebar && (
          <div className="relative flex shrink-0 flex-col border-r bg-muted/20" style={{ width: sideW }}>
            {view === "explorer" && <ExplorerView key={root} />}
            {view === "search" && <SearchView />}
            {view === "scm" && <ScmView />}
            <div
              onPointerDown={(e) => dragResize(e, sideW, "x", 1, (n) => Math.min(Math.max(170, n), 700), setSideW)}
              className="absolute top-0 right-[-3px] bottom-0 z-10 w-1.5 cursor-col-resize hover:bg-primary/30"
            />
          </div>
        )}

        {/* Editor and panel */}
        <div className="flex min-w-0 flex-1 flex-col">
          <Tabs />
          <Breadcrumbs />
          <div className="relative min-h-0 flex-1">
            <div ref={codeEl} className="absolute inset-0" />
            <div ref={diffEl} className="absolute inset-0" style={{ display: "none" }} />
            {(!hasTabs || !ready) && (
              <div className="absolute inset-0 grid place-items-center bg-background">
                {loadErr ? (
                  <div className="text-sm text-destructive">{loadErr}</div>
                ) : !ready ? (
                  <div className="flex items-center gap-2 text-sm text-muted-foreground">
                    <LoaderCircleIcon className="size-4 animate-spin" /> Loading the editor…
                  </div>
                ) : (
                  <Welcome root={root} />
                )}
              </div>
            )}
          </div>
          {panel && (
            <div className="relative shrink-0 border-t" style={{ height: panelH }}>
              <div
                onPointerDown={(e) => dragResize(e, panelH, "y", -1, (n) => Math.min(Math.max(100, n), Math.round(window.innerHeight * 0.75)), setPanelH)}
                className="absolute top-[-3px] right-0 left-0 z-10 h-1.5 cursor-row-resize hover:bg-primary/30"
              />
              <TerminalPanel root={root} onClose={() => togglePanel(false)} />
            </div>
          )}
        </div>
      </div>
      <StatusBar onBranch={() => setView("scm")} />
      <QuickOpenHost commands={commands} />
      <ConflictDialog />
    </div>
  );
}

/** QuickOpenHost remounts the box per opening, so it starts from its text. */
function QuickOpenHost({ commands }: { commands: Command[] }) {
  const quick = useEditor((s) => s.quick);
  if (!quick) return null;
  return <QuickOpen key={quick.mode + quick.text} commands={commands} />;
}

function Welcome({ root }: { root: string }) {
  const keys: [string, string][] = [
    ["Go to File", "Ctrl+P"],
    ["Command Palette", "Ctrl+Shift+P"],
    ["Search in Files", "Ctrl+Shift+F"],
    ["Source Control", "Ctrl+Shift+G"],
    ["Toggle Terminal", "Ctrl+`"],
    ["Toggle Side Bar", "Ctrl+B"],
    ["Save · Save All", "Ctrl+S · Ctrl+Alt+S"],
    ["Close Editor", "Alt+W"],
  ];
  return (
    <div className="text-center">
      <div className="mb-1 text-lg font-medium">{root.split(/[\\/]/).filter(Boolean).pop()}</div>
      <div className="mb-5 font-mono text-xs text-muted-foreground">{root}</div>
      <div className="grid grid-cols-[auto_auto] gap-x-6 gap-y-1.5 text-left text-[13px]">
        {keys.map(([l, k]) => (
          <React.Fragment key={l}>
            <span className="text-muted-foreground">{l}</span>
            <kbd className="font-mono text-[12px]">{k}</kbd>
          </React.Fragment>
        ))}
      </div>
    </div>
  );
}
