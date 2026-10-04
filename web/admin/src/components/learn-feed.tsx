"use client";

import Link from "next/link";
import { cn } from "@/lib/utils";
import type { Activity } from "@/lib/types";
import { Ago } from "@/components/common";

const stageStyle: Record<string, string> = {
  extract: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  scenes: "bg-violet-500/15 text-violet-700 dark:text-violet-300",
  persona: "bg-fuchsia-500/15 text-fuchsia-700 dark:text-fuchsia-300",
  skill: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  error: "bg-destructive/15 text-destructive",
  note: "bg-muted text-muted-foreground",
};

const stageLabel: Record<string, string> = {
  extract: "L1 extract",
  scenes: "L2 scenes",
  persona: "L3 persona",
  skill: "skill",
  error: "error",
  note: "note",
};

/** ActivityItem is one step of the learner, linked both ways: to the session it
 * learned from and to the memories it wrote. */
export function ActivityItem({ a, compact }: { a: Activity; compact?: boolean }) {
  const [head, ...rest] = (a.error || a.detail || "").split("\n");
  return (
    <div className={cn("flex gap-3 rounded-lg px-2 py-1.5 text-sm hover:bg-muted/40", !compact && "border-b last:border-b-0")}>
      <span className={cn("mt-0.5 h-fit shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium", stageStyle[a.stage] ?? stageStyle.note)}>
        {stageLabel[a.stage] ?? a.stage}
      </span>
      <div className="min-w-0 flex-1">
        <div className={cn("break-words", a.error && "text-destructive", compact && "truncate")}>{head}</div>
        {!compact && rest.length > 0 && (
          <ul className="mt-1 space-y-0.5 text-xs text-muted-foreground">
            {rest.map((l, i) => (
              <li key={i} className="break-words">
                {l}
              </li>
            ))}
          </ul>
        )}
        <div className="mt-0.5 flex flex-wrap items-center gap-x-3 text-xs text-muted-foreground">
          <Ago at={a.at} />
          {a.session && (
            <Link className="hover:text-foreground hover:underline" href={`/sessions?id=${a.session}`}>
              from session {a.session.slice(0, 15)}…
            </Link>
          )}
          {a.records && a.records.length > 0 && (
            <Link className="hover:text-foreground hover:underline" href={`/memory?ids=${a.records.join(",")}`}>
              {a.records.length} memor{a.records.length > 1 ? "ies" : "y"} written →
            </Link>
          )}
        </div>
      </div>
    </div>
  );
}
