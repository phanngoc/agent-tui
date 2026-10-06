"use client";

import * as React from "react";
import { TextQuoteIcon } from "lucide-react";

/**
 * SelectionAction is the Claude app's "selection as context": select text in
 * the conversation and a button over it adds the selection to the next
 * message, as a quote above the composer.
 */
export function SelectionAction({
  container,
  onAdd,
}: {
  container: React.RefObject<HTMLElement | null>;
  onAdd: (text: string) => void;
}) {
  const [sel, setSel] = React.useState<{
    text: string;
    x: number;
    y: number;
    below: boolean;
  } | null>(null);

  React.useEffect(() => {
    let frame = 0;
    const update = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        const s = document.getSelection();
        const el = container.current;
        if (!s || s.isCollapsed || !s.rangeCount || !el) return setSel(null);
        const range = s.getRangeAt(0);
        if (!el.contains(range.commonAncestorContainer)) return setSel(null);
        const text = s.toString().trim();
        if (!text) return setSel(null);
        const r = range.getBoundingClientRect();
        if (!r.width && !r.height) return setSel(null);
        const below = r.top < 90; // no room above: under the selection
        setSel({
          text,
          x: Math.min(
            Math.max(r.left + r.width / 2, 70),
            window.innerWidth - 70,
          ),
          y: below ? r.bottom + 8 : r.top - 8,
          below,
        });
      });
    };
    document.addEventListener("selectionchange", update);
    window.addEventListener("scroll", update, true);
    window.addEventListener("resize", update);
    return () => {
      cancelAnimationFrame(frame);
      document.removeEventListener("selectionchange", update);
      window.removeEventListener("scroll", update, true);
      window.removeEventListener("resize", update);
    };
  }, [container]);

  if (!sel) return null;
  return (
    <button
      type="button"
      // Pressing must not clear the selection before the click lands.
      onMouseDown={(e) => e.preventDefault()}
      onClick={() => {
        onAdd(sel.text);
        document.getSelection()?.removeAllRanges();
        setSel(null);
      }}
      style={{ left: sel.x, top: sel.y }}
      className={`fixed z-40 inline-flex -translate-x-1/2 items-center gap-1.5 rounded-full border bg-popover px-3 py-1.5 text-xs font-medium text-popover-foreground shadow-lg hover:bg-muted ${sel.below ? "" : "-translate-y-full"}`}
    >
      <TextQuoteIcon className="size-3.5" /> Add to chat
    </button>
  );
}

/** quoteBlock is a selection as the next message carries it: a Markdown quote. */
export function quoteBlock(text: string): string {
  return text
    .split("\n")
    .map((l) => (l.trim() ? "> " + l : ">"))
    .join("\n");
}

/** withQuotes puts the quoted selections ahead of what was typed. */
export function withQuotes(quotes: string[], text: string): string {
  const head = quotes.map(quoteBlock).join("\n\n");
  return [head, text.trim()].filter(Boolean).join("\n\n");
}
