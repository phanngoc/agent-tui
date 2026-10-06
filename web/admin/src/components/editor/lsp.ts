"use client";

import type * as Monaco from "monaco-editor";
import { api, gatewayBase, qs } from "@/lib/api";
import type { MonacoNS } from "@/lib/monaco";
import { editorService } from "./service";
import { useEditor } from "./store";

// A language server client for Monaco, small on purpose. The gateway runs the
// server in the project (typescript-language-server, with the project's own
// TypeScript) and pipes JSON-RPC: messages come in on an event stream and go
// out by post. Here they are matched to Monaco's providers — completion,
// hover, signature help, definition, references, rename, formatting,
// outline — and diagnostics become markers. Because the server reads the
// whole project (tsconfig, node_modules, every file), all of it is
// cross-file, which Monaco's own in-page TypeScript cannot be.
//
// It starts on the first TypeScript or JavaScript file opened, as opencode
// starts a server on first touch, and Monaco's own TypeScript features are
// switched off once it is ready, so nothing is offered twice.

type Json = Record<string, unknown>;
type Pending = { resolve: (v: unknown) => void; reject: (e: Error) => void; timer: ReturnType<typeof setTimeout> };

const LANGS: Record<string, string> = { typescript: "typescript", javascript: "javascript" };
const lspLanguageId = (model: Monaco.editor.ITextModel) => {
  const path = model.uri.path.toLowerCase();
  if (path.endsWith(".tsx")) return "typescriptreact";
  if (path.endsWith(".jsx")) return "javascriptreact";
  return model.getLanguageId();
};

export type LspState = { state: "off" | "starting" | "loading" | "ready" | "error"; message?: string };

export class LspClient {
  private monaco: MonacoNS;
  private root: string;
  private id = "";
  private rootUri = "";
  private es: EventSource | null = null;
  private seq = 0;
  private pending = new Map<number, Pending>();
  private chain: Promise<unknown> = Promise.resolve();
  private open = new Map<string, { model: Monaco.editor.ITextModel; changes: Json[]; timer: ReturnType<typeof setTimeout> | null; subs: Monaco.IDisposable[] }>();
  private disposables: Monaco.IDisposable[] = [];
  private disposed = false;
  private restarts = 0;
  private caps: Json = {};
  private ready: Promise<void> | null = null;

  constructor(monaco: MonacoNS, root: string, private onState: (s: LspState) => void) {
    this.monaco = monaco;
    this.root = root;
  }

  // ---- connection ---------------------------------------------------------

  start(): Promise<void> {
    if (this.ready) return this.ready;
    this.ready = this.connect().catch((e) => {
      this.onState({ state: "error", message: (e as Error).message });
      this.ready = null;
      throw e;
    });
    return this.ready;
  }

  private async connect() {
    this.onState({ state: "starting", message: "starting the TypeScript language server…" });
    const r = await api.post<{ id: string; root_uri: string; initialization_options: Json }>("/api/lsp/start", { root: this.root, server: "typescript" });
    if (this.disposed) {
      void api.del(`/api/lsp/${r.id}`).catch(() => undefined);
      return;
    }
    this.id = r.id;
    this.rootUri = r.root_uri;
    await new Promise<void>((resolve, reject) => {
      const es = new EventSource(gatewayBase() + `/api/lsp/${r.id}/stream`);
      this.es = es;
      let opened = false;
      es.onopen = () => {
        if (!opened) {
          opened = true;
          resolve();
        }
      };
      es.onmessage = (e) => this.receive(e.data);
      es.addEventListener("exit", (e) => this.exited(JSON.parse((e as MessageEvent).data || '""')));
      es.onerror = () => {
        if (!opened) reject(new Error("could not reach the language server"));
      };
    });
    const init = (await this.request("initialize", {
      processId: null,
      rootUri: this.rootUri,
      workspaceFolders: [{ uri: this.rootUri, name: this.root.split(/[\\/]/).filter(Boolean).pop() }],
      initializationOptions: r.initialization_options,
      capabilities: {
        workspace: { applyEdit: true, workspaceEdit: { documentChanges: true }, configuration: true, workspaceFolders: true },
        // The server says when it is loading the project; until it has, its
        // answers are the syntax server's, which stops at an import.
        window: { workDoneProgress: true },
        textDocument: {
          synchronization: { didSave: true, dynamicRegistration: false },
          completion: {
            completionItem: { snippetSupport: true, documentationFormat: ["markdown", "plaintext"], resolveSupport: { properties: ["documentation", "detail", "additionalTextEdits"] }, insertReplaceSupport: false },
            contextSupport: true,
          },
          hover: { contentFormat: ["markdown", "plaintext"] },
          signatureHelp: { signatureInformation: { documentationFormat: ["markdown", "plaintext"], parameterInformation: { labelOffsetSupport: true } } },
          definition: { linkSupport: false },
          typeDefinition: { linkSupport: false },
          implementation: { linkSupport: false },
          references: {},
          documentSymbol: { hierarchicalDocumentSymbolSupport: true },
          formatting: {},
          rangeFormatting: {},
          rename: { prepareSupport: false },
          publishDiagnostics: { relatedInformation: true, tagSupport: { valueSet: [1, 2] } },
        },
      },
    }, 60000)) as Json;
    this.caps = (init?.capabilities as Json) ?? {};
    this.notify("initialized", {});
    this.registerProviders();
    for (const m of this.monaco.editor.getModels()) this.didOpen(m);
    this.disposables.push(this.monaco.editor.onDidCreateModel((m) => this.didOpen(m)));
    this.disposables.push(this.monaco.editor.onWillDisposeModel((m) => this.didClose(m)));
    this.restarts = 0;
    if (!this.loading.size) this.onState({ state: "ready", message: "TypeScript language server" });
  }

  private exited(why: string) {
    this.es?.close();
    this.es = null;
    for (const [, p] of this.pending) p.reject(new Error("the language server stopped"));
    this.pending.clear();
    if (this.disposed) return;
    // Back with a fresh server, a few times; then say why it keeps stopping.
    if (this.restarts < 3) {
      this.restarts++;
      this.teardown(false);
      this.ready = null;
      this.onState({ state: "starting", message: "restarting the language server…" });
      setTimeout(() => void this.start().catch(() => undefined), 1000 * this.restarts);
    } else this.onState({ state: "error", message: "the language server keeps stopping: " + why });
  }

  private post(msg: Json) {
    const body = JSON.stringify({ jsonrpc: "2.0", ...msg });
    // In order: each post waits for the one before.
    this.chain = this.chain
      .then(() => fetch(gatewayBase() + `/api/lsp/${this.id}/send`, { method: "POST", body, headers: { "Content-Type": "application/json" } }))
      .catch(() => undefined);
  }

  request(method: string, params: unknown, timeout = 15000): Promise<unknown> {
    const id = ++this.seq;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(method + " timed out"));
      }, timeout);
      this.pending.set(id, { resolve, reject, timer });
      this.post({ id, method, params });
    });
  }

  notify(method: string, params: unknown) {
    this.post({ method, params });
  }

  private receive(data: string) {
    let msg: Json;
    try {
      msg = JSON.parse(data);
    } catch {
      return;
    }
    const id = msg.id as number | string | undefined;
    if (id !== undefined && msg.method === undefined) {
      const p = this.pending.get(id as number);
      if (!p) return;
      this.pending.delete(id as number);
      clearTimeout(p.timer);
      if (msg.error) p.reject(new Error(((msg.error as Json).message as string) ?? "error"));
      else p.resolve(msg.result);
      return;
    }
    if (id !== undefined) return void this.answer(id, msg.method as string, msg.params as Json);
    if (msg.method === "textDocument/publishDiagnostics") this.diagnostics(msg.params as Json);
    else if (msg.method === "$/progress") this.progress(msg.params as Json);
  }

  private loading = new Set<string | number>();

  /** progress follows the server's work — loading the project above all. */
  private progress(p: Json) {
    const v = (p?.value as Json) ?? {};
    const token = p?.token as string | number;
    if (v.kind === "begin") {
      this.loading.add(token);
      this.onState({ state: "loading", message: ((v.title as string) || "loading the project") + (v.message ? ": " + v.message : "") });
    } else if (v.kind === "end") {
      this.loading.delete(token);
      if (!this.loading.size) this.onState({ state: "ready", message: "TypeScript language server" });
    }
  }

  /** answer replies to what the server asks of the client. */
  private async answer(id: number | string, method: string, params: Json) {
    let result: unknown = null;
    if (method === "workspace/configuration") result = ((params?.items as unknown[]) ?? []).map(() => ({}));
    else if (method === "workspace/workspaceFolders") result = [{ uri: this.rootUri, name: "root" }];
    else if (method === "workspace/applyEdit") result = { applied: await this.applyWorkspaceEdit(params.edit as Json) };
    this.post({ id, result });
  }

  // ---- documents ----------------------------------------------------------

  /** uri is a project file's URI in the server's namespace. */
  uri(path: string) {
    return this.rootUri + "/" + path.split("/").map(encodeURIComponent).join("/");
  }

  /** pathOf is the project-relative path a server URI names, if it is in the project. */
  pathOf(uri: string): string | null {
    const norm = (u: string) => {
      let s = decodeURIComponent(u.replace(/^file:\/\//, ""));
      s = s.replace(/^\/([A-Za-z]):/, (_, d: string) => "/" + d.toLowerCase() + ":");
      return s;
    };
    const root = norm(this.rootUri).replace(/\/$/, "");
    const p = norm(uri);
    const win = /^\/[a-z]:/.test(root);
    const a = win ? p.toLowerCase() : p;
    const b = win ? root.toLowerCase() : root;
    if (!a.startsWith(b + "/")) return null;
    return p.slice(root.length + 1);
  }

  private pathOfModel(model: Monaco.editor.ITextModel): string | null {
    return model.uri.scheme === "file" ? model.uri.path.replace(/^\//, "") : null;
  }

  private modelFor(uri: string): Monaco.editor.ITextModel | null {
    const p = this.pathOf(uri);
    return p ? this.monaco.editor.getModel(this.monaco.Uri.from({ scheme: "file", path: "/" + p })) : null;
  }

  private didOpen(model: Monaco.editor.ITextModel) {
    const path = this.pathOfModel(model);
    if (!path || !LANGS[model.getLanguageId()] || this.open.has(path)) return;
    const entry = { model, changes: [] as Json[], timer: null as ReturnType<typeof setTimeout> | null, subs: [] as Monaco.IDisposable[] };
    this.open.set(path, entry);
    this.notify("textDocument/didOpen", { textDocument: { uri: this.uri(path), languageId: lspLanguageId(model), version: model.getVersionId(), text: model.getValue() } });
    entry.subs.push(
      model.onDidChangeContent((e) => {
        for (const c of e.changes)
          entry.changes.push({ range: { start: { line: c.range.startLineNumber - 1, character: c.range.startColumn - 1 }, end: { line: c.range.endLineNumber - 1, character: c.range.endColumn - 1 } }, rangeLength: c.rangeLength, text: c.text });
        // Keystrokes travel together: one didChange per pause, not per key.
        if (!entry.timer) entry.timer = setTimeout(() => this.flush(path), 40);
      }),
    );
  }

  /** flush sends a document's pending changes, before anything asks about it. */
  private flush(path: string) {
    const e = this.open.get(path);
    if (!e) return;
    if (e.timer) clearTimeout(e.timer);
    e.timer = null;
    if (!e.changes.length) return;
    const changes = e.changes;
    e.changes = [];
    this.notify("textDocument/didChange", { textDocument: { uri: this.uri(path), version: e.model.getVersionId() }, contentChanges: changes });
  }

  private didClose(model: Monaco.editor.ITextModel) {
    const path = this.pathOfModel(model);
    const e = path ? this.open.get(path) : null;
    if (!path || !e) return;
    this.flush(path);
    e.subs.forEach((d) => d.dispose());
    this.open.delete(path);
    this.monaco.editor.setModelMarkers(model, "lsp", []);
    this.notify("textDocument/didClose", { textDocument: { uri: this.uri(path) } });
  }

  /** saved tells the server a file was written. */
  saved(path: string) {
    if (this.open.has(path)) this.notify("textDocument/didSave", { textDocument: { uri: this.uri(path) } });
  }

  private ask(model: Monaco.editor.ITextModel, method: string, extra: Json = {}, pos?: Monaco.Position) {
    const path = this.pathOfModel(model);
    if (!path || !this.open.has(path)) return Promise.resolve(null);
    this.flush(path);
    const params: Json = { textDocument: { uri: this.uri(path) }, ...extra };
    if (pos) params.position = { line: pos.lineNumber - 1, character: pos.column - 1 };
    return this.request(method, params).catch(() => null);
  }

  // ---- conversions --------------------------------------------------------

  private range(r: Json): Monaco.IRange {
    const s = r.start as Json;
    const e = r.end as Json;
    return { startLineNumber: (s.line as number) + 1, startColumn: (s.character as number) + 1, endLineNumber: (e.line as number) + 1, endColumn: (e.character as number) + 1 };
  }

  private markdown(c: unknown): Monaco.IMarkdownString[] {
    if (!c) return [];
    if (typeof c === "string") return [{ value: c }];
    if (Array.isArray(c)) return c.flatMap((x) => this.markdown(x));
    const o = c as Json;
    if (o.kind === "markdown" || o.kind === "plaintext") return [{ value: o.kind === "plaintext" ? "```\n" + o.value + "\n```" : (o.value as string) }];
    if (o.language) return [{ value: "```" + o.language + "\n" + o.value + "\n```" }];
    return [];
  }

  private location(l: Json): Monaco.languages.Location | null {
    const uri = (l.uri ?? l.targetUri) as string;
    const range = (l.range ?? l.targetSelectionRange ?? l.targetRange) as Json;
    const p = this.pathOf(uri);
    if (!p || !range) return null;
    return { uri: this.monaco.Uri.from({ scheme: "file", path: "/" + p }), range: this.range(range) };
  }

  private locations(r: unknown): Monaco.languages.Location[] {
    if (!r) return [];
    const list = Array.isArray(r) ? r : [r];
    return list.map((l) => this.location(l as Json)).filter((x): x is Monaco.languages.Location => !!x);
  }

  /** applyWorkspaceEdit opens the files an edit touches, then applies it, undoably. */
  async applyWorkspaceEdit(edit: Json | null): Promise<boolean> {
    if (!edit) return false;
    const byUri = new Map<string, Json[]>();
    for (const [uri, edits] of Object.entries((edit.changes as Record<string, Json[]>) ?? {})) byUri.set(uri, edits);
    for (const dc of (edit.documentChanges as Json[]) ?? []) {
      if (dc.textDocument) byUri.set((dc.textDocument as Json).uri as string, (dc.edits as Json[]) ?? []);
    }
    let ok = true;
    for (const [uri, edits] of byUri) {
      const path = this.pathOf(uri);
      if (!path) {
        ok = false;
        continue;
      }
      let model = this.modelFor(uri);
      if (!model) {
        await editorService.open(path, { focus: false, preview: false });
        model = this.modelFor(uri);
      }
      if (!model) {
        ok = false;
        continue;
      }
      model.pushEditOperations([], edits.map((e) => ({ range: this.range(e.range as Json), text: e.newText as string })), () => null);
    }
    return ok;
  }


  private completionKind(k?: number): Monaco.languages.CompletionItemKind {
    const K = this.monaco.languages.CompletionItemKind;
    const map: Record<number, Monaco.languages.CompletionItemKind> = {
      1: K.Text, 2: K.Method, 3: K.Function, 4: K.Constructor, 5: K.Field, 6: K.Variable, 7: K.Class, 8: K.Interface, 9: K.Module,
      10: K.Property, 11: K.Unit, 12: K.Value, 13: K.Enum, 14: K.Keyword, 15: K.Snippet, 16: K.Color, 17: K.File, 18: K.Reference,
      19: K.Folder, 20: K.EnumMember, 21: K.Constant, 22: K.Struct, 23: K.Event, 24: K.Operator, 25: K.TypeParameter,
    };
    return (k && map[k]) ?? K.Text;
  }

  private symbolKind(k?: number): Monaco.languages.SymbolKind {
    // LSP's SymbolKind is Monaco's plus one.
    return ((k ?? 13) - 1) as Monaco.languages.SymbolKind;
  }

  // ---- Monaco providers ---------------------------------------------------

  private registerProviders() {
    const m = this.monaco;
    const langs = Object.keys(LANGS);
    const items = new WeakMap<Monaco.languages.CompletionItem, Json>();
    const triggers = ((this.caps.completionProvider as Json)?.triggerCharacters as string[]) ?? [".", '"', "'", "/", "@", "<"];
    for (const lang of langs) {
      this.disposables.push(
        m.languages.registerCompletionItemProvider(lang, {
          triggerCharacters: triggers,
          provideCompletionItems: async (model, pos, ctx) => {
            const r = (await this.ask(model, "textDocument/completion", { context: { triggerKind: ctx.triggerKind + 1, triggerCharacter: ctx.triggerCharacter } }, pos)) as Json | Json[] | null;
            if (!r) return { suggestions: [] };
            const list = Array.isArray(r) ? r : ((r.items as Json[]) ?? []);
            const word = model.getWordUntilPosition(pos);
            const fallback = { startLineNumber: pos.lineNumber, startColumn: word.startColumn, endLineNumber: pos.lineNumber, endColumn: pos.column };
            const suggestions = list.map((it) => {
              const te = it.textEdit as Json | undefined;
              const s: Monaco.languages.CompletionItem = {
                label: it.label as string,
                kind: this.completionKind(it.kind as number),
                detail: it.detail as string | undefined,
                documentation: this.markdown(it.documentation)[0],
                sortText: it.sortText as string | undefined,
                filterText: it.filterText as string | undefined,
                preselect: it.preselect as boolean | undefined,
                insertText: ((te?.newText as string) ?? (it.insertText as string) ?? (it.label as string)) || "",
                insertTextRules: it.insertTextFormat === 2 ? m.languages.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
                range: te?.range ? this.range(te.range as Json) : fallback,
                additionalTextEdits: ((it.additionalTextEdits as Json[]) ?? []).map((e) => ({ range: this.range(e.range as Json), text: e.newText as string })),
                commitCharacters: it.commitCharacters as string[] | undefined,
              };
              items.set(s, it);
              return s;
            });
            return { suggestions, incomplete: !Array.isArray(r) && !!r.isIncomplete };
          },
          resolveCompletionItem: async (item) => {
            const raw = items.get(item);
            if (!raw || !(this.caps.completionProvider as Json)?.resolveProvider) return item;
            const r = (await this.request("completionItem/resolve", raw).catch(() => null)) as Json | null;
            if (!r) return item;
            item.documentation = this.markdown(r.documentation)[0] ?? item.documentation;
            item.detail = (r.detail as string) ?? item.detail;
            if (r.additionalTextEdits) item.additionalTextEdits = (r.additionalTextEdits as Json[]).map((e) => ({ range: this.range(e.range as Json), text: e.newText as string }));
            return item;
          },
        }),
        m.languages.registerHoverProvider(lang, {
          provideHover: async (model, pos) => {
            const r = (await this.ask(model, "textDocument/hover", {}, pos)) as Json | null;
            if (!r) return null;
            return { contents: this.markdown(r.contents), range: r.range ? this.range(r.range as Json) : undefined };
          },
        }),
        m.languages.registerSignatureHelpProvider(lang, {
          signatureHelpTriggerCharacters: ["(", ","],
          signatureHelpRetriggerCharacters: [")"],
          provideSignatureHelp: async (model, pos) => {
            const r = (await this.ask(model, "textDocument/signatureHelp", {}, pos)) as Json | null;
            if (!r) return null;
            return {
              value: {
                activeSignature: (r.activeSignature as number) ?? 0,
                activeParameter: (r.activeParameter as number) ?? 0,
                signatures: ((r.signatures as Json[]) ?? []).map((sg) => ({
                  label: sg.label as string,
                  documentation: this.markdown(sg.documentation)[0],
                  parameters: ((sg.parameters as Json[]) ?? []).map((p) => ({ label: p.label as string | [number, number], documentation: this.markdown(p.documentation)[0] })),
                })),
              },
              dispose() {},
            };
          },
        }),
        m.languages.registerDefinitionProvider(lang, {
          provideDefinition: async (model, pos) => this.locations(await this.ask(model, "textDocument/definition", {}, pos)),
        }),
        m.languages.registerTypeDefinitionProvider(lang, {
          provideTypeDefinition: async (model, pos) => this.locations(await this.ask(model, "textDocument/typeDefinition", {}, pos)),
        }),
        m.languages.registerImplementationProvider(lang, {
          provideImplementation: async (model, pos) => this.locations(await this.ask(model, "textDocument/implementation", {}, pos)),
        }),
        m.languages.registerReferenceProvider(lang, {
          provideReferences: async (model, pos) => this.locations(await this.ask(model, "textDocument/references", { context: { includeDeclaration: true } }, pos)),
        }),
        m.languages.registerRenameProvider(lang, {
          provideRenameEdits: async (model, pos, newName) => {
            const r = (await this.ask(model, "textDocument/rename", { newName }, pos)) as Json | null;
            // Files the rename reaches are opened first, so the edit can land in them.
            await this.applyWorkspaceEdit(r);
            return { edits: [] };
          },
        }),
        m.languages.registerDocumentFormattingEditProvider(lang, {
          provideDocumentFormattingEdits: async (model, opts) => {
            const r = (await this.ask(model, "textDocument/formatting", { options: { tabSize: opts.tabSize, insertSpaces: opts.insertSpaces } })) as Json[] | null;
            return (r ?? []).map((e) => ({ range: this.range(e.range as Json), text: e.newText as string }));
          },
        }),
        m.languages.registerDocumentSymbolProvider(lang, {
          provideDocumentSymbols: async (model) => {
            const r = (await this.ask(model, "textDocument/documentSymbol")) as Json[] | null;
            const conv = (s: Json): Monaco.languages.DocumentSymbol => ({
              name: s.name as string,
              detail: (s.detail as string) ?? "",
              kind: this.symbolKind(s.kind as number),
              tags: [],
              range: this.range((s.range ?? (s.location as Json)?.range) as Json),
              selectionRange: this.range((s.selectionRange ?? s.range ?? (s.location as Json)?.range) as Json),
              children: ((s.children as Json[]) ?? []).map(conv),
            });
            return (r ?? []).map(conv);
          },
        }),
      );
    }
  }

  private diagnostics(p: Json) {
    const model = this.modelFor(p.uri as string);
    if (!model) return;
    const S = this.monaco.MarkerSeverity;
    const sev: Record<number, Monaco.MarkerSeverity> = { 1: S.Error, 2: S.Warning, 3: S.Info, 4: S.Hint };
    const markers = ((p.diagnostics as Json[]) ?? []).map((d) => {
      const r = this.range(d.range as Json);
      return {
        ...r,
        severity: sev[(d.severity as number) ?? 1] ?? S.Error,
        message: d.message as string,
        source: (d.source as string) ?? "ts",
        code: d.code !== undefined ? String(d.code) : undefined,
        tags: ((d.tags as number[]) ?? []).map((t) => (t === 1 ? this.monaco.MarkerTag.Unnecessary : this.monaco.MarkerTag.Deprecated)),
      };
    });
    this.monaco.editor.setModelMarkers(model, "lsp", markers);
  }

  // ---- lifetime -----------------------------------------------------------

  private teardown(final: boolean) {
    for (const [, e] of this.open) {
      e.subs.forEach((d) => d.dispose());
      this.monaco.editor.setModelMarkers(e.model, "lsp", []);
    }
    this.open.clear();
    this.disposables.forEach((d) => d.dispose());
    this.disposables = [];
    if (final && this.id) void api.del(`/api/lsp/${this.id}`).catch(() => undefined);
  }

  dispose() {
    this.disposed = true;
    this.es?.close();
    this.teardown(true);
    for (const [, p] of this.pending) clearTimeout(p.timer);
    this.pending.clear();
  }
}

// ---- the workbench's one client --------------------------------------------

/**
 * quietBuiltin turns off Monaco's own per-file TypeScript features, which the
 * server has project-wide. Monaco registers them once, when the first
 * TypeScript model appears, and never looks at the setting again — so this
 * must run before any such model exists, or both answer and every definition
 * comes back twice (and opens as a peek instead of a jump).
 */
function quietBuiltin(monaco: MonacoNS) {
  const ts = (monaco as unknown as { typescript?: Record<string, { setModeConfiguration?: (c: Json) => void }> }).typescript;
  const off = {
    completionItems: false, hovers: false, documentSymbols: false, definitions: false, references: false, documentHighlights: true,
    rename: false, diagnostics: false, documentRangeFormattingEdits: false, signatureHelp: false, onTypeFormattingEdits: false, codeActions: false, inlayHints: false,
  };
  ts?.typescriptDefaults?.setModeConfiguration?.(off);
  ts?.javascriptDefaults?.setModeConfiguration?.(off);
}

/**
 * prepareLsp asks whether a language server can run for the project, before
 * the first file opens. If it can, Monaco's own TypeScript is switched off and
 * the server starts with the first TS/JS file; if not, Monaco's per-file
 * IntelliSense stays, and the status bar says why.
 */
export async function prepareLsp(monaco: MonacoNS, root: string): Promise<void> {
  try {
    const r = await api.get<{ ok: boolean; reason?: string }>("/api/lsp/check" + qs({ root }));
    if (!r.ok) {
      useEditor.getState().setLsp({ state: "error", message: (r.reason ?? "no language server") + " — per-file IntelliSense only" });
      return;
    }
  } catch {
    return;
  }
  quietBuiltin(monaco);
  startLsp(monaco, root);
}

let client: LspClient | null = null;
let opener: Monaco.IDisposable | null = null;
let watch: Monaco.IDisposable | null = null;

/** startLsp watches for the first TypeScript or JavaScript file and starts the server for it. */
export function startLsp(monaco: MonacoNS, root: string) {
  stopLsp();
  const setState = (s: LspState) => useEditor.getState().setLsp(s);
  // Definitions in other files open in the editor's own tabs.
  opener = monaco.editor.registerEditorOpener({
    openCodeEditor(_source, resource, sel) {
      if (resource.scheme !== "file") return false;
      const path = resource.path.replace(/^\//, "");
      const pos = sel && "startLineNumber" in sel ? { line: sel.startLineNumber, col: sel.startColumn } : sel && "lineNumber" in sel ? { line: sel.lineNumber, col: sel.column } : {};
      void editorService.open(path, pos);
      return true;
    },
  });
  const maybeStart = (m: Monaco.editor.ITextModel) => {
    if (client || m.uri.scheme !== "file" || !LANGS[m.getLanguageId()]) return;
    client = new LspClient(monaco, root, setState);
    void client.start().catch(() => undefined);
    watch?.dispose();
    watch = null;
  };
  for (const m of monaco.editor.getModels()) maybeStart(m);
  if (!client) watch = monaco.editor.onDidCreateModel(maybeStart);
}

export function stopLsp() {
  client?.dispose();
  client = null;
  opener?.dispose();
  opener = null;
  watch?.dispose();
  watch = null;
  useEditor.getState().setLsp({ state: "off" });
}

export function lspSaved(path: string) {
  client?.saved(path);
}
