"use client";

import { create } from "zustand";

// The workbench's state that React draws: tabs, which view is showing, the
// cursor for the status bar, the open palette. The documents themselves —
// Monaco models, view states, save points — live in the editor service,
// outside React, so typing never re-renders the page.

export type Tab = {
  /** key is the path for a file, "diff:" + path for its diff against HEAD. */
  key: string;
  path: string;
  kind: "file" | "diff";
  dirty: boolean;
  /** preview tabs are replaced by the next file opened, as in VS Code. */
  preview: boolean;
  /** the file changed on disk while it had unsaved edits. */
  stale?: boolean;
  readOnly?: boolean;
  /** pinned tabs stay at the front and outlive Close Others and Close All. */
  pinned?: boolean;
};

export type View = "explorer" | "search" | "scm";
export type Quick = { mode: "files" | "commands" | "line"; text: string } | null;
export type Conflict = { path: string } | null;

interface EditorState {
  root: string;
  tabs: Tab[];
  active: string | null;
  view: View;
  sidebar: boolean;
  panel: boolean;
  cursor: { line: number; col: number; selected: number };
  lang: string;
  eol: string;
  indent: string;
  quick: Quick;
  conflict: Conflict;
  branch: string;
  lsp: { state: "off" | "starting" | "loading" | "ready" | "error"; message?: string };
  setLsp: (s: EditorState["lsp"]) => void;
  /** revealReq asks the explorer to show a file (Reveal in Explorer View). */
  revealReq: { path: string; seq: number } | null;
  revealFile: (path: string) => void;
  setPinned: (key: string, pinned: boolean) => void;
  setRoot: (r: string) => void;
  upsertTab: (t: Tab, opts?: { activate?: boolean }) => void;
  closeTab: (key: string) => void;
  patchTab: (key: string, p: Partial<Tab>) => void;
  setActive: (key: string | null) => void;
  setView: (v: View) => void;
  toggleSidebar: (on?: boolean) => void;
  togglePanel: (on?: boolean) => void;
  setCursor: (c: EditorState["cursor"]) => void;
  setDocInfo: (i: { lang: string; eol: string; indent: string }) => void;
  setQuick: (q: Quick) => void;
  setConflict: (c: Conflict) => void;
  setBranch: (b: string) => void;
}

export const useEditor = create<EditorState>((set) => ({
  root: "",
  tabs: [],
  active: null,
  view: "explorer",
  sidebar: true,
  panel: false,
  cursor: { line: 1, col: 1, selected: 0 },
  lang: "",
  eol: "LF",
  indent: "",
  quick: null,
  conflict: null,
  branch: "",
  lsp: { state: "off" },
  setLsp: (lsp) => set({ lsp }),
  revealReq: null,
  revealFile: (path) => set((s) => ({ view: "explorer", sidebar: true, revealReq: { path, seq: (s.revealReq?.seq ?? 0) + 1 } })),
  setPinned: (key, pinned) =>
    set((s) => {
      const t = s.tabs.find((x) => x.key === key);
      if (!t) return s;
      const rest = s.tabs.filter((x) => x.key !== key);
      const tab = { ...t, pinned, preview: false };
      // Pinned tabs lead, in the order they were pinned; unpinning puts a
      // tab first among the others.
      const lead = rest.filter((x) => x.pinned);
      const tail = rest.filter((x) => !x.pinned);
      return { tabs: pinned ? [...lead, tab, ...tail] : [...lead, tab, ...tail] };
    }),
  setRoot: (root) => set({ root, tabs: [], active: null }),
  upsertTab: (t, opts) =>
    set((s) => {
      const i = s.tabs.findIndex((x) => x.key === t.key);
      let tabs = s.tabs;
      if (i >= 0) {
        tabs = s.tabs.slice();
        // Opening a preview tab's file again for real keeps it.
        tabs[i] = { ...s.tabs[i], ...t, preview: s.tabs[i].preview && t.preview };
      } else {
        // A new preview replaces the old preview, where it stood.
        const p = t.preview ? s.tabs.findIndex((x) => x.preview && !x.dirty) : -1;
        if (p >= 0) {
          tabs = s.tabs.slice();
          tabs[p] = t;
        } else {
          const at = s.active ? s.tabs.findIndex((x) => x.key === s.active) + 1 : s.tabs.length;
          tabs = [...s.tabs.slice(0, at), t, ...s.tabs.slice(at)];
        }
      }
      return { tabs, active: opts?.activate === false ? s.active : t.key };
    }),
  closeTab: (key) =>
    set((s) => {
      const i = s.tabs.findIndex((x) => x.key === key);
      if (i < 0) return s;
      const tabs = s.tabs.filter((x) => x.key !== key);
      const active = s.active === key ? (tabs[i] ?? tabs[i - 1] ?? null)?.key ?? null : s.active;
      return { tabs, active };
    }),
  patchTab: (key, p) => set((s) => ({ tabs: s.tabs.map((x) => (x.key === key ? { ...x, ...p } : x)) })),
  setActive: (active) => set({ active }),
  setView: (view) => set({ view, sidebar: true }),
  toggleSidebar: (on) => set((s) => ({ sidebar: on ?? !s.sidebar })),
  togglePanel: (on) => set((s) => ({ panel: on ?? !s.panel })),
  setCursor: (cursor) => set({ cursor }),
  setDocInfo: (i) => set(i),
  setQuick: (quick) => set({ quick }),
  setConflict: (conflict) => set({ conflict }),
  setBranch: (branch) => set({ branch }),
}));
