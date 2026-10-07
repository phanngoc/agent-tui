import { create } from "zustand";

// Which conversations have news this viewer has not read: a turn that ended
// while they were looking elsewhere, or one they marked unread themselves.
// It is this browser's, as reading is: kept in localStorage.

const KEY = "agent-tui.unread";

interface Saved {
  /** seen is each conversation's update time when it was last read. */
  seen: Record<string, string>;
  /** marked are the ones marked unread by hand. */
  marked: Record<string, true>;
}

function load(): Saved {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? "");
    if (v && typeof v === "object") return { seen: v.seen ?? {}, marked: v.marked ?? {} };
  } catch {}
  return { seen: {}, marked: {} };
}

function save(s: Saved) {
  try {
    localStorage.setItem(KEY, JSON.stringify(s));
  } catch {}
}

interface UnreadState extends Saved {
  loaded: boolean;
  /** know records the conversations a list shows: one never seen before counts as read. */
  know: (list: { id: string; updated: string }[]) => void;
  read: (id: string, updated: string) => void;
  markUnread: (id: string) => void;
}

export const useUnread = create<UnreadState>((set, get) => ({
  seen: {},
  marked: {},
  loaded: false,
  know: (list) => {
    const cur = get().loaded ? { seen: get().seen, marked: get().marked } : load();
    let changed = !get().loaded;
    const seen = { ...cur.seen };
    for (const s of list) {
      if (!(s.id in seen)) {
        seen[s.id] = s.updated;
        changed = true;
      }
    }
    if (!changed) return;
    const next = { seen, marked: cur.marked };
    set({ ...next, loaded: true });
    save(next);
  },
  read: (id, updated) => {
    const { seen, marked } = get();
    if (seen[id] === updated && !marked[id]) return;
    const m = { ...marked };
    delete m[id];
    const next = { seen: { ...seen, [id]: updated }, marked: m };
    set(next);
    save(next);
  },
  markUnread: (id) => {
    const next = { seen: get().seen, marked: { ...get().marked, [id]: true as const } };
    set(next);
    save(next);
  },
}));

/** isUnread says whether a conversation has news for this viewer. */
export function isUnread(st: Saved, s: { id: string; updated: string; busy?: boolean }) {
  if (st.marked[s.id]) return true;
  const seen = st.seen[s.id];
  return !s.busy && seen !== undefined && new Date(s.updated).getTime() > new Date(seen).getTime() + 1000;
}
