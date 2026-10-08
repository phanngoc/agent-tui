"use client";

import * as React from "react";
import { cn } from "@/lib/utils";
import type { ToolCall } from "@/lib/types";

/** A file change a call made, read from its input: Claude Code's Edit,
 * MultiEdit and Write, and the built-in engine's edit_file and write_file
 * (same fields, `path` for `file_path`). */
export interface FileChange {
  path: string;
  /** write: the whole file was written; edit: parts of it were replaced. */
  kind: "write" | "edit";
  hunks: { old: string; new: string }[];
}

export function fileChange(call: ToolCall): FileChange | undefined {
  let input = call.input;
  if (typeof input === "string") {
    try {
      input = JSON.parse(input);
    } catch {
      return undefined;
    }
  }
  if (!input || typeof input !== "object") return undefined;
  const o = input as Record<string, unknown>;
  const path = typeof o.file_path === "string" ? o.file_path : typeof o.path === "string" ? o.path : typeof o.notebook_path === "string" ? o.notebook_path : "";
  if (!path) return undefined;
  if (typeof o.old_string === "string" && typeof o.new_string === "string") return { path, kind: "edit", hunks: [{ old: o.old_string, new: o.new_string }] };
  if (Array.isArray(o.edits)) {
    const hunks = o.edits
      .filter((e): e is Record<string, unknown> => !!e && typeof e === "object")
      .filter((e) => typeof e.old_string === "string" && typeof e.new_string === "string")
      .map((e) => ({ old: e.old_string as string, new: e.new_string as string }));
    return hunks.length ? { path, kind: "edit", hunks } : undefined;
  }
  if (typeof o.content === "string" && /write/i.test(call.name)) return { path, kind: "write", hunks: [{ old: "", new: o.content }] };
  if (typeof o.new_source === "string" && /notebook/i.test(call.name)) return { path, kind: "edit", hunks: [{ old: "", new: o.new_source }] };
  return undefined;
}

type Line = { op: " " | "-" | "+"; text: string };

const splitLines = (s: string) => (s === "" ? [] : s.replace(/\n$/, "").split("\n"));

/** lineDiff is the line-by-line difference of two texts (longest common
 * subsequence). Past a size where that gets slow, it says "all of a went,
 * all of b came" — still true, only less tidy. */
export function lineDiff(a: string, b: string): Line[] {
  const x = splitLines(a);
  const y = splitLines(b);
  const n = x.length;
  const m = y.length;
  if (n * m > 400_000) return [...x.map((t) => ({ op: "-" as const, text: t })), ...y.map((t) => ({ op: "+" as const, text: t }))];
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--) lcs[i][j] = x[i] === y[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
  const out: Line[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (x[i] === y[j]) {
      out.push({ op: " ", text: x[i] });
      i++;
      j++;
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) out.push({ op: "-", text: x[i++] });
    else out.push({ op: "+", text: y[j++] });
  }
  while (i < n) out.push({ op: "-", text: x[i++] });
  while (j < m) out.push({ op: "+", text: y[j++] });
  return out;
}

/** counts is what a change adds and removes, in lines. */
export function changeCounts(c: FileChange): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const h of c.hunks)
    for (const l of lineDiff(h.old, h.new)) {
      if (l.op === "+") added++;
      else if (l.op === "-") removed++;
    }
  return { added, removed };
}

const CONTEXT = 3;

/** fold keeps the lines near a change and folds long unchanged stretches
 * into one marker, as a diff viewer does. */
function fold(lines: Line[]): (Line | { skip: number })[] {
  const near = lines.map(() => false);
  lines.forEach((l, i) => {
    if (l.op === " ") return;
    for (let k = Math.max(0, i - CONTEXT); k <= Math.min(lines.length - 1, i + CONTEXT); k++) near[k] = true;
  });
  const out: (Line | { skip: number })[] = [];
  for (let i = 0; i < lines.length; ) {
    if (near[i]) {
      out.push(lines[i++]);
      continue;
    }
    let j = i;
    while (j < lines.length && !near[j]) j++;
    out.push({ skip: j - i });
    i = j;
  }
  return out;
}

/** The longest a written file shows before the rest is folded. */
const WRITE_PREVIEW = 24;

/** DiffView draws a file change the way Claude Code does: removed lines red
 * with "-", added green with "+", a few lines of context around them. */
export function DiffView({ change }: { change: FileChange }) {
  const [all, setAll] = React.useState(false);
  return (
    <div className="max-h-[28rem] overflow-auto rounded-md border bg-muted/30 font-mono text-xs leading-5">
      {change.hunks.map((h, hi) => {
        let rows: (Line | { skip: number })[] = fold(lineDiff(h.old, h.new));
        if (change.kind === "write" && !all && rows.length > WRITE_PREVIEW) rows = [...rows.slice(0, WRITE_PREVIEW), { skip: rows.length - WRITE_PREVIEW }];
        return (
          <div key={hi} className={cn(hi > 0 && "border-t border-dashed")}>
            {rows.map((r, i) =>
              "skip" in r ? (
                <button
                  key={i}
                  className="block w-full px-2 text-left text-muted-foreground hover:bg-muted"
                  onClick={() => change.kind === "write" && setAll(true)}
                  disabled={change.kind !== "write"}
                >
                  ⋯ {r.skip} {r.skip === 1 ? "line" : "lines"} {change.kind === "write" ? "more" : "unchanged"}
                </button>
              ) : (
                <div
                  key={i}
                  className={cn(
                    "flex whitespace-pre",
                    r.op === "-" && "bg-red-500/10 text-red-800 dark:text-red-300",
                    r.op === "+" && "bg-emerald-500/10 text-emerald-800 dark:text-emerald-300",
                  )}
                >
                  <span className="w-5 shrink-0 text-center opacity-70 select-none">{r.op === " " ? "" : r.op}</span>
                  <span className="pr-2">{r.text || " "}</span>
                </div>
              ),
            )}
          </div>
        );
      })}
    </div>
  );
}
