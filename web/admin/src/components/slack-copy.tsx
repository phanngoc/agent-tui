"use client";

import * as React from "react";
import { flushSync } from "react-dom";
import { createRoot } from "react-dom/client";
import { Markdown } from "@/components/markdown";
import { copyRich, slackFrom } from "@/lib/slack";

// Copying for Slack: a selection, or a whole answer.

/**
 * selectionFragment is what a selection covers, inside the elements it sits
 * in: a few words picked from a code block are still code, a row picked from
 * a table is still a table row. The selection's own contents are wrapped in a
 * bare copy of each element around it, up to the transcript.
 */
export function selectionFragment(range: Range, within: Node): Node {
  let frag: Node = range.cloneContents();
  let at: Node | null = range.commonAncestorContainer;
  if (at.nodeType !== Node.ELEMENT_NODE) at = at.parentNode;
  while (at && at !== within && within.contains(at)) {
    const shell = at.cloneNode(false);
    shell.appendChild(frag);
    frag = shell;
    at = at.parentNode;
  }
  const box = document.createElement("div");
  box.appendChild(frag);
  return box;
}

/** copySelectionForSlack copies what is selected inside within, for Slack.
 * It says whether there was anything to copy. */
export async function copySelectionForSlack(within: Node): Promise<boolean> {
  const s = document.getSelection();
  if (!s || s.isCollapsed || !s.rangeCount) return false;
  const { html, text } = slackFrom(selectionFragment(s.getRangeAt(0), within));
  if (!text.trim()) return false;
  await copyRich(html, text);
  return true;
}

/** copyMarkdownForSlack copies a message's Markdown for Slack, rendered the
 * way the transcript renders it and then converted from that. */
export async function copyMarkdownForSlack(markdown: string): Promise<void> {
  const box = document.createElement("div");
  const root = createRoot(box);
  flushSync(() => root.render(<Markdown text={markdown} />));
  const { html, text } = slackFrom(box);
  root.unmount();
  await copyRich(html, text);
}
