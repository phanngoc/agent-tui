import { create } from "zustand";

// Text waiting to be put in the next chat composer that opens: "Add File to
// Chat" from the editor puts a file's path here and goes to a new chat,
// whose composer takes it.

interface DraftState {
  pending: string;
  push: (text: string) => void;
  take: () => string;
}

export const useChatDraft = create<DraftState>((set, get) => ({
  pending: "",
  push: (text) => set((s) => ({ pending: s.pending ? s.pending + " " + text : text })),
  take: () => {
    const t = get().pending;
    if (t) set({ pending: "" });
    return t;
  },
}));
