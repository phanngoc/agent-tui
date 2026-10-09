"use client";

import * as React from "react";
import { HashIcon, LibraryIcon, TextQuoteIcon } from "lucide-react";
import { toast } from "sonner";
import { copySelectionForSlack } from "@/components/slack-copy";

/**
 * SelectionAction is the Claude app's "selection as context": select text in
 * the conversation and a button over it adds the selection to the next
 * message, as a quote above the composer. Beside it, "Copy for Slack" copies
 * the selection formatted to paste into Slack, for the team.
 */
export function SelectionAction({
  container,
  onAdd,
  onWiki,
}: {
  container: React.RefObject<HTMLElement | null>;
  onAdd: (text: string) => void;
  /** onWiki puts the selection into the project's wiki. */
  onWiki?: (text: string) => void;
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
            Math.max(r.left + r.width / 2, 130),
            window.innerWidth - 130,
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
  const item = "inline-flex items-center gap-1.5 px-3 py-1.5 hover:bg-muted";
  return (
    <div
      // Pressing must not clear the selection before the click lands.
      onMouseDown={(e) => e.preventDefault()}
      style={{ left: sel.x, top: sel.y }}
      className={`fixed z-40 inline-flex -translate-x-1/2 divide-x overflow-hidden rounded-full border bg-popover text-xs font-medium text-popover-foreground shadow-lg ${sel.below ? "" : "-translate-y-full"}`}
    >
      <button
        type="button"
        className={item}
        onClick={() => {
          onAdd(sel.text);
          document.getSelection()?.removeAllRanges();
          setSel(null);
        }}
      >
        <TextQuoteIcon className="size-3.5" /> Add to chat
      </button>
      {onWiki && (
        <button
          type="button"
          className={item}
          title="Put the selection into the project's wiki: kept as a document and read into its pages"
          onClick={() => {
            onWiki(sel.text);
            document.getSelection()?.removeAllRanges();
            setSel(null);
          }}
        >
          <LibraryIcon className="size-3.5" /> Add to wiki
        </button>
      )}
      <button
        type="button"
        className={item}
        title="Copy the selection formatted for Slack: bold, lists, links and code come through; headings turn bold and tables into an aligned block"
        onClick={() => {
          const el = container.current;
          if (!el) return;
          copySelectionForSlack(el).then(
            (ok) => {
              if (ok) toast.success("Copied for Slack", { description: "Paste it into a Slack message." });
              else toast("Nothing to copy in that selection");
            },
            (e: Error) => toast.error(`Could not copy: ${e.message}`),
          );
        }}
      >
        <HashIcon className="size-3.5" /> Copy for Slack
      </button>
    </div>
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
