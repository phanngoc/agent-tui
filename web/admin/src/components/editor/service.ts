"use client";

import type * as Monaco from "monaco-editor";
import { toast } from "sonner";
import { api, ApiError, qs } from "@/lib/api";
import { languageFor, loadMonaco, type MonacoNS } from "@/lib/monaco";
import { useEditor, type Tab } from "./store";
import { lspSaved, prepareLsp, stopLsp } from "./lsp";

// The editor service: one Monaco editor and one diff editor for the whole
// workbench, and a document per open file — its model, the modification time
// it was read at, the version it was last saved at, and where the view was.
// Switching tabs swaps the model and restores the view, as VS Code does; no
// editor is created or destroyed per tab, and nothing here re-renders React
// on a keystroke: the store hears only what the chrome shows (dirty, cursor).

type Doc = {
  model: Monaco.editor.ITextModel;
  base: number; // modification time on disk when read or saved
  saved: number; // the model's alternative version id at the last save
  view: Monaco.editor.ICodeEditorViewState | null;
  readOnly: boolean;
  head?: Monaco.editor.ITextModel; // HEAD's text, for the diff
};

const READ_MAX = 50 << 20;

class EditorService {
  monaco: MonacoNS | null = null;
  editor: Monaco.editor.IStandaloneCodeEditor | null = null;
  diff: Monaco.editor.IStandaloneDiffEditor | null = null;
  private docs = new Map<string, Doc>();
  private pending = new Map<string, Promise<Doc | null>>();
  private shown: string | null = null;
  private codeEl: HTMLElement | null = null;
  private gate: Promise<void> = Promise.resolve();
  private diffEl: HTMLElement | null = null;

  get root() {
    return useEditor.getState().root;
  }

  /** attach creates the editors in the given elements, once Monaco is loaded. */
  async attach(codeEl: HTMLElement, diffEl: HTMLElement, dark: boolean) {
    const monaco = await loadMonaco();
    this.monaco = monaco;
    this.codeEl = codeEl;
    this.diffEl = diffEl;
    const common: Monaco.editor.IEditorOptions = {
      automaticLayout: true,
      fontFamily: "'Cascadia Code', 'Cascadia Mono', Consolas, 'Geist Mono', monospace",
      fontSize: 13,
      fontLigatures: true,
      minimap: { enabled: true, renderCharacters: false, maxColumn: 100 },
      smoothScrolling: true,
      cursorSmoothCaretAnimation: "on",
      renderWhitespace: "selection",
      bracketPairColorization: { enabled: true },
      guides: { bracketPairs: "active", indentation: true },
      stickyScroll: { enabled: true },
      scrollBeyondLastLine: false,
      mouseWheelZoom: true,
      // Large files stay fast on their own: Monaco's largeFileOptimizations
      // is on by default and turns off what costs.
    };
    monaco.editor.setTheme(dark ? "vs-dark" : "vs");
    this.editor = monaco.editor.create(codeEl, { ...common, model: null });
    this.diff = monaco.editor.createDiffEditor(diffEl, { ...common, renderSideBySide: true, originalEditable: false, enableSplitViewResizing: true });

    const ed = this.editor;
    ed.onDidChangeCursorSelection(() => this.reportCursor(ed));
    ed.onDidChangeModel(() => {
      this.reportCursor(ed);
      this.reportDocInfo(ed.getModel());
    });
    // Ctrl+S inside the editor, where the page's own shortcut does not reach
    // first on every browser.
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => void this.saveActive());
    // A language server for the project, from its first TS/JS file. Files
    // wait for the decision: Monaco's own TypeScript must be off before the
    // first TS model, or it can never be turned off.
    this.gate = prepareLsp(monaco, this.root);
    const active = useEditor.getState().active;
    if (active) void this.show(active);
  }

  setTheme(dark: boolean) {
    this.monaco?.editor.setTheme(dark ? "vs-dark" : "vs");
  }

  private reportCursor(ed: Monaco.editor.ICodeEditor) {
    const sel = ed.getSelection();
    const model = ed.getModel();
    if (!sel || !model) return;
    useEditor.getState().setCursor({ line: sel.positionLineNumber, col: sel.positionColumn, selected: model.getValueInRange(sel).length });
  }

  private reportDocInfo(model: Monaco.editor.ITextModel | null) {
    if (!model) return;
    const o = model.getOptions();
    useEditor.getState().setDocInfo({
      lang: model.getLanguageId(),
      eol: model.getEOL() === "\r\n" ? "CRLF" : "LF",
      indent: o.insertSpaces ? `Spaces: ${o.tabSize}` : `Tab Size: ${o.tabSize}`,
    });
  }

  private uri(path: string) {
    return this.monaco!.Uri.from({ scheme: "file", path: "/" + path });
  }

  /** load reads a file into a document, once, however many ask at once. */
  private load(path: string): Promise<Doc | null> {
    const have = this.docs.get(path);
    if (have) return Promise.resolve(have);
    const inflight = this.pending.get(path);
    if (inflight) return inflight;
    const p = (async () => {
      const monaco = this.monaco ?? (await loadMonaco());
      this.monaco = monaco;
      await this.gate;
      try {
        const f = await api.get<{ text?: string; binary: boolean; truncated: boolean; mtime: number; size: number }>(
          "/api/files/read" + qs({ root: this.root, path, max: READ_MAX }),
        );
        if (f.binary) {
          toast.error(`${path} is a binary file`);
          return null;
        }
        const model = monaco.editor.createModel(f.text ?? "", languageFor(monaco, path), this.uri(path));
        // Indentation as the file has it, not as the editor would like.
        model.detectIndentation(true, 4);
        const doc: Doc = { model, base: f.mtime, saved: model.getAlternativeVersionId(), view: null, readOnly: f.truncated };
        if (f.truncated) toast.warning(`${path} is larger than 50 MB: opened read-only`);
        model.onDidChangeContent(() => this.markDirty(path));
        this.docs.set(path, doc);
        return doc;
      } catch (e) {
        toast.error((e as Error).message);
        return null;
      } finally {
        this.pending.delete(path);
      }
    })();
    this.pending.set(path, p);
    return p;
  }

  private markDirty(path: string) {
    const doc = this.docs.get(path);
    if (!doc) return;
    const dirty = doc.model.getAlternativeVersionId() !== doc.saved;
    const st = useEditor.getState();
    for (const t of st.tabs) if (t.path === path && t.dirty !== dirty) st.patchTab(t.key, { dirty, preview: dirty ? false : t.preview });
  }

  isDirty(path: string) {
    const doc = this.docs.get(path);
    return !!doc && doc.model.getAlternativeVersionId() !== doc.saved;
  }

  /** open shows a file, at a line when given; preview tabs give way to the next. */
  async open(path: string, opts: { line?: number; col?: number; endCol?: number; preview?: boolean; focus?: boolean } = {}) {
    const st = useEditor.getState();
    const existing = st.tabs.find((t) => t.key === path);
    st.upsertTab({ key: path, path, kind: "file", dirty: existing?.dirty ?? false, preview: opts.preview ?? false, readOnly: existing?.readOnly });
    const doc = await this.load(path);
    if (!doc) {
      useEditor.getState().closeTab(path);
      return;
    }
    if (doc.readOnly) useEditor.getState().patchTab(path, { readOnly: true });
    if (useEditor.getState().active === path) this.show(path);
    if (opts.line && this.editor) {
      const line = Math.max(1, opts.line);
      const range = new this.monaco!.Range(line, opts.col ?? 1, line, opts.endCol ?? opts.col ?? 1);
      this.editor.setSelection(range);
      this.editor.revealRangeInCenterIfOutsideViewport(range, 0);
    }
    if (opts.focus !== false) this.editor?.focus();
  }

  /** openDiff shows a file against HEAD; the right side is the file itself, editable. */
  async openDiff(path: string) {
    const key = "diff:" + path;
    useEditor.getState().upsertTab({ key, path, kind: "diff", dirty: false, preview: false });
    const doc = await this.load(path);
    if (!doc) return useEditor.getState().closeTab(key);
    const head = await api.get<{ text: string }>("/api/git/head" + qs({ root: this.root, path })).catch(() => ({ text: "" }));
    doc.head?.dispose();
    doc.head = this.monaco!.editor.createModel(head.text, doc.model.getLanguageId());
    if (useEditor.getState().active === key) this.show(key);
  }

  /** show puts a tab's document in front, keeping where the last one was. */
  show(key: string) {
    if (!this.editor || !this.diff || !this.codeEl || !this.diffEl) return;
    const prev = this.shown && !this.shown.startsWith("diff:") ? this.docs.get(this.shown) : null;
    if (prev && this.editor.getModel() === prev.model) prev.view = this.editor.saveViewState();
    const isDiff = key.startsWith("diff:");
    const path = isDiff ? key.slice(5) : key;
    const doc = this.docs.get(path);
    this.codeEl.style.display = isDiff ? "none" : "block";
    this.diffEl.style.display = isDiff ? "block" : "none";
    this.shown = key;
    if (!doc) return;
    if (isDiff) {
      if (doc.head) this.diff.setModel({ original: doc.head, modified: doc.model });
      this.diff.layout();
      this.reportDocInfo(doc.model);
      return;
    }
    if (this.editor.getModel() !== doc.model) {
      this.editor.setModel(doc.model);
      if (doc.view) this.editor.restoreViewState(doc.view);
    }
    this.editor.updateOptions({ readOnly: doc.readOnly });
    this.editor.layout();
  }

  /** close lets a tab go; its document goes when no tab shows it. */
  close(key: string) {
    const st = useEditor.getState();
    const tab = st.tabs.find((t) => t.key === key);
    if (!tab) return;
    if (tab.kind === "file" && this.isDirty(tab.path) && !confirm(`${tab.path} has unsaved changes. Close it and lose them?`)) return;
    st.closeTab(key);
    const still = useEditor.getState().tabs.some((t) => t.path === tab.path);
    if (!still) {
      const doc = this.docs.get(tab.path);
      if (doc) {
        if (this.editor?.getModel() === doc.model) this.editor.setModel(null);
        if (this.diff?.getModel()?.modified === doc.model) this.diff.setModel(null);
        doc.head?.dispose();
        doc.model.dispose();
        this.docs.delete(tab.path);
      }
    }
    const next = useEditor.getState().active;
    if (next) this.show(next);
    else if (this.editor) this.editor.setModel(null);
  }

  activePath(): string | null {
    const a = useEditor.getState().active;
    return a ? (a.startsWith("diff:") ? a.slice(5) : a) : null;
  }

  async saveActive() {
    const p = this.activePath();
    if (p) await this.save(p);
  }

  /** save writes a file, unless it changed on disk since it was read. */
  async save(path: string, force = false): Promise<boolean> {
    const doc = this.docs.get(path);
    if (!doc || doc.readOnly) return false;
    const version = doc.model.getAlternativeVersionId();
    try {
      const r = await api.put<{ modified: number }>("/api/files/write", { root: this.root, path, text: doc.model.getValue(), base: doc.base, force });
      doc.base = r.modified;
      doc.saved = version;
      this.markDirty(path);
      lspSaved(path);
      for (const t of useEditor.getState().tabs) if (t.path === path) useEditor.getState().patchTab(t.key, { stale: false });
      return true;
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        useEditor.getState().setConflict({ path });
        return false;
      }
      toast.error(`Saving ${path}: ${(e as Error).message}`);
      return false;
    }
  }

  async saveAll() {
    let n = 0;
    for (const [path] of this.docs) if (this.isDirty(path) && (await this.save(path))) n++;
    if (n) toast.success(`Saved ${n} file${n === 1 ? "" : "s"}`);
  }

  /** reload replaces a document with what is on disk, keeping the view. */
  async reload(path: string) {
    const doc = this.docs.get(path);
    if (!doc) return;
    const f = await api.get<{ text?: string; truncated: boolean; mtime: number }>("/api/files/read" + qs({ root: this.root, path, max: READ_MAX }));
    const view = this.editor?.getModel() === doc.model ? this.editor.saveViewState() : null;
    // pushEditOperations keeps it undoable, as VS Code's revert is.
    doc.model.pushEditOperations([], [{ range: doc.model.getFullModelRange(), text: f.text ?? "" }], () => null);
    doc.saved = doc.model.getAlternativeVersionId();
    doc.base = f.mtime;
    if (view) this.editor?.restoreViewState(view);
    this.markDirty(path);
    for (const t of useEditor.getState().tabs) if (t.path === path) useEditor.getState().patchTab(t.key, { stale: false });
  }

  /** checkDisk notices open files changed elsewhere — by the agent, a terminal,
   * git — and reloads those without edits; those with edits are marked. */
  async checkDisk() {
    const paths = [...this.docs.keys()];
    if (!paths.length || !this.root) return;
    const r = await api.post<{ modified: Record<string, number> }>("/api/files/stat", { root: this.root, paths }).catch(() => null);
    if (!r) return;
    for (const path of paths) {
      const doc = this.docs.get(path);
      const m = r.modified[path];
      if (!doc || m === undefined || m === doc.base || doc.base === 0) continue;
      if (m === -1) {
        for (const t of useEditor.getState().tabs) if (t.path === path) useEditor.getState().patchTab(t.key, { stale: true });
        continue;
      }
      if (this.isDirty(path)) {
        for (const t of useEditor.getState().tabs) if (t.path === path) useEditor.getState().patchTab(t.key, { stale: true });
      } else await this.reload(path);
    }
  }

  /** renamed moves open documents after a file or folder was renamed. */
  renamed(from: string, to: string) {
    for (const [path, doc] of [...this.docs]) {
      if (path !== from && !path.startsWith(from + "/")) continue;
      const next = to + path.slice(from.length);
      const model = this.monaco!.editor.createModel(doc.model.getValue(), languageFor(this.monaco!, next), this.uri(next));
      model.onDidChangeContent(() => this.markDirty(next));
      const wasShown = this.editor?.getModel() === doc.model;
      if (wasShown) this.editor!.setModel(model);
      doc.model.dispose();
      this.docs.delete(path);
      this.docs.set(next, { ...doc, model, saved: model.getAlternativeVersionId() });
      const st = useEditor.getState();
      for (const t of st.tabs)
        if (t.path === path) {
          st.closeTab(t.key);
          st.upsertTab({ ...t, key: t.kind === "diff" ? "diff:" + next : next, path: next }, { activate: wasShown });
        }
    }
  }

  /** deleted closes the tabs of a deleted file or folder. */
  deleted(p: string) {
    for (const t of [...useEditor.getState().tabs]) {
      if (t.path === p || t.path.startsWith(p + "/")) {
        const doc = this.docs.get(t.path);
        if (doc) doc.saved = doc.model.getAlternativeVersionId(); // no unsaved prompt for a file that is gone
        this.close(t.key);
      }
    }
  }

  anyDirty() {
    for (const [path] of this.docs) if (this.isDirty(path)) return true;
    return false;
  }

  /** run triggers a Monaco action on the editor (format, find, go to line…). */
  run(action: string) {
    this.editor?.focus();
    void this.editor?.getAction(action)?.run();
  }

  reset() {
    stopLsp();
    for (const [, d] of this.docs) {
      d.head?.dispose();
      d.model.dispose();
    }
    this.docs.clear();
    this.editor?.setModel(null);
    this.diff?.setModel(null);
    this.shown = null;
  }
}

export const editorService = new EditorService();
export type { Tab };
