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
  /\.(md|mdx|go|ts|tsx|js|jsx|mjs|cjs|py|rb|rs|java|kt|php|cs|c|h|cpp|hpp|sh|ps1|sql|json|ya?ml|toml|ini|env|conf|xml|html|css|scss|txt|log|csv|mod|sum|lock|gradle|properties|dockerfile|tf|proto|graphql|svg|png|jpe?g|gif|webp|pem|crt|cer|csr|pub)(:\d+(:\d+)?)?$/i;

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

// Absolute, or the home's: "~/keys/a.pem" is resolved by the gateway.
const isAbsolute = (p: string) => /^([/\\~]|[A-Za-z]:[/\\])/.test(p);

// pathToken finds the paths a text names: absolute on either system, a WSL
// share, or the home's — in a command, a tool's output, or a reply.
const pathToken = /(?<![\w.$-])(?:~|[A-Za-z]:|\\\\[^\s\\/]+)?(?:[/\\][^\s'"`<>|;,(){}[\]*?]+)+/g;

/** stringsOf is every string in a tool's input, however deep. */
function stringsOf(v: unknown, out: string[] = [], depth = 0): string[] {
  if (depth > 4) return out;
  if (typeof v === "string") out.push(v);
  else if (Array.isArray(v)) v.forEach((x) => stringsOf(x, out, depth + 1));
  else if (v && typeof v === "object") Object.values(v).forEach((x) => stringsOf(x, out, depth + 1));
  return out;
}

// folderToken finds the folders a text names, absolute or not, as a reply
// does before listing what is in one: "Documents\x\test-public\ gồm a.json".
const folderToken = /(?<![\w.$:/\\-])(?:~[/\\]|[A-Za-z]:[/\\]|\\\\|\/)?(?:[\w.@+-]+[/\\]){2,}/g;

/** inFolderSaid is name in a folder the same text names, when it names one. */
function inFolderSaid(text: string, name: string): string | undefined {
  if (!text.includes(name)) return undefined;
  for (const m of text.matchAll(folderToken)) {
    if (/^\/\//.test(m[0])) continue; // a URL's host, not a folder
    return m[0] + name;
  }
  return undefined;
}

/** pathEnding is the first path in a text that ends with name, or nothing. */
function pathEnding(text: string, name: string): string | undefined {
  for (const m of text.matchAll(pathToken)) {
    const tok = m[0].replace(/[.:]+$/, "");
    if (isAbsolute(tok) && tok.replace(/\\/g, "/").endsWith("/" + name)) return tok;
  }
  return undefined;
}

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
  const line = m?.[2] ?? "";
  // A file a tool wrote or read by name, latest first.
  for (let i = messages.length - 1; i >= 0; i--) {
    const cs = calls(messages[i].tools);
    for (let j = cs.length - 1; j >= 0; j--) {
      const f = filePathOf(cs[j].input);
      if (f && isAbsolute(f) && f.replace(/\\/g, "/").endsWith("/" + name)) return f + line;
    }
  }
  // Then the latest reply that names it: by its whole path, or beside the
  // folder it is in ("…\test-public\ gồm public.pem, public.jwk.json"). The
  // gateway finds a relative folder in the project or the home.
  for (let i = messages.length - 1; i >= 0; i--) {
    const text = messages[i].text ?? "";
    if (!text.includes(name)) continue;
    const f = pathEnding(text, name) ?? inFolderSaid(text, name);
    if (f) return f + line;
    break;
  }
  // Then any path that ends with it in what the tools did: a command that
  // copied the file, the output that listed it.
  for (let i = messages.length - 1; i >= 0; i--) {
    const cs = calls(messages[i].tools);
    for (let j = cs.length - 1; j >= 0; j--) {
      for (const s of [...stringsOf(cs[j].input), cs[j].result ?? ""]) {
        const f = pathEnding(s, name);
        if (f) return f + line;
      }
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
