"use client";

import { Maximize2Icon, Minimize2Icon } from "lucide-react";
import { useLayout } from "@/lib/layout";

/** FocusButton folds every panel for reading, and puts them back (Alt+\). */
export function FocusButton() {
  const focus = useLayout((s) => s.focus);
  const setFocus = useLayout((s) => s.setFocus);
  return (
    <button
      type="button"
      onClick={() => setFocus(!focus)}
      title={focus ? "Leave focus mode — bring the panels back (Alt+\)" : "Focus mode — fold every panel to read (Alt+\)"}
      className="inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
    >
      {focus ? <Minimize2Icon className="size-4" /> : <Maximize2Icon className="size-4" />}
    </button>
  );
}
