"use client";

import * as React from "react";
import type { Message, ToolCall } from "@/lib/types";

// A conversation's paths open in the project explorer: the session page
// provides the opener, and anything that shows a path — a reply's inline
// code, a tool call — asks for it. Outside a session there is none, and
// paths stay plain text.

/** The file opener a conversation hands its paths to; null outside a session. */
export const FileOpener = React.createContext<((p: string) => void) | null>(null);

export function useFileOpener() {
  return React.useContext(FileOpener);
}

const pathish = /^[\w.@~+\-/\\:]+$/;
const knownExt =
  /\.(md|mdx|go|ts|tsx|js|jsx|mjs|cjs|py|rb|rs|java|kt|php|cs|c|h|cpp|hpp|sh|ps1|sql|json|ya?ml|toml|ini|env|conf|xml|html|css|scss|txt|log|csv|mod|sum|lock|gradle|properties|dockerfile|tf|proto|graphql|svg|png|jpe?g|gif|webp)(:\d+(:\d+)?)?$/i;

/** filePathOf is the file a tool call worked on, when it names one. */
export function filePathOf(input: unknown): string | undefined {
  if (!input || typeof input !== "object") return undefined;
  const o = input as Record<string, unknown>;
  for (const k of ["file_path", "path", "notebook_path", "filePath"]) {
    const v = o[k];
    if (typeof v === "string" && v.trim() && /\.[A-Za-z0-9]{1,8}$/.test(v)) return v;
  }
  return undefined;
}

const isAbsolute = (p: string) => /^([/\\]|[A-Za-z]:[/\\])/.test(p);

/**
 * fullPathOf is the file a bare name or a relative path in a reply means,
 * found among the files the conversation's tools worked on — the latest one
 * whose path it is the end of — so a file written outside the project opens
 * too. Its line, if it named one, is kept.
 */
export function fullPathOf(messages: Message[], p: string): string | undefined {
  const t = p.trim();
  const m = t.match(/^(.*?)(:\d+(?::\d+)?)?$/);
  const name = (m?.[1] ?? t).replace(/\\/g, "/").replace(/^\.\//, "");
  if (!name || isAbsolute(name)) return undefined;
  const calls = (list: ToolCall[] | undefined): ToolCall[] => (list ?? []).flatMap((c) => [c, ...calls(c.agent?.calls)]);
  for (let i = messages.length - 1; i >= 0; i--) {
    const cs = calls(messages[i].tools);
    for (let j = cs.length - 1; j >= 0; j--) {
      const f = filePathOf(cs[j].input);
      if (f && isAbsolute(f) && f.replace(/\\/g, "/").endsWith("/" + name)) return f + (m?.[2] ?? "");
    }
  }
  return undefined;
}

/** looksLikePath says an inline code span is probably a file in the project. */
export function looksLikePath(s: string) {
  const t = s.trim();
  if (t.length < 3 || t.length > 300 || !pathish.test(t) || /^https?:/.test(t)) return false;
  return knownExt.test(t) || (/[/\\]/.test(t) && /\.[A-Za-z0-9]{1,8}(:\d+)?$/.test(t));
}
