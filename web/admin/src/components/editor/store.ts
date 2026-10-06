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
