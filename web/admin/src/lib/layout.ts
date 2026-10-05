"use client";

import { create } from "zustand";

// What the reader has folded away, remembered per browser: the navigation
// rail, and focus mode, which folds every side panel at once for reading.

function read(key: string, fallback: boolean): boolean {
  try {
    const v = localStorage.getItem(key);
    return v === null ? fallback : v === "1";
  } catch {
    return fallback;
  }
}

function write(key: string, v: boolean) {
  try {
    localStorage.setItem(key, v ? "1" : "0");
  } catch {}
}

interface LayoutState {
  ready: boolean;
  navCollapsed: boolean;
  focus: boolean;
  load: () => void;
  toggleNav: () => void;
  setFocus: (on: boolean) => void;
}

export const useLayout = create<LayoutState>((set, get) => ({
  ready: false,
  navCollapsed: false,
  focus: false,
  load: () => {
    if (get().ready) return;
    // A narrow window starts with the rail folded, whatever was saved.
    const narrow = typeof window !== "undefined" && window.innerWidth < 1100;
    set({ ready: true, navCollapsed: narrow || read("agent-tui.nav.collapsed", false) });
  },
  toggleNav: () => {
    const v = !get().navCollapsed;
    write("agent-tui.nav.collapsed", v);
    set({ navCollapsed: v });
  },
  setFocus: (on) => set({ focus: on }),
}));

/** A storage for panel layouts that never throws, for private windows. */
export const safeStorage = {
  getItem(key: string): string | null {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  setItem(key: string, value: string) {
    try {
      localStorage.setItem(key, value);
    } catch {}
  },
};

/** isTyping reports whether a key press belongs to a text field. */
export function isTyping(e: KeyboardEvent): boolean {
  const t = e.target as HTMLElement | null;
  return !!t && (t.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(t.tagName));
}
