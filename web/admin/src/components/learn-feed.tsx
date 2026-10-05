"use client";

import Link from "next/link";
import { cn } from "@/lib/utils";
import type { Activity } from "@/lib/types";
import { Ago } from "@/components/common";

export interface StoreInfo {
  name: string;
  root?: string;
  scope: "global" | "project";
}

const stageStyle: Record<string, string> = {
  extract: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  scenes: "bg-violet-500/15 text-violet-700 dark:text-violet-300",
  persona: "bg-fuchsia-500/15 text-fuchsia-700 dark:text-fuchsia-300",
  skill: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  error: "bg-destructive/15 text-destructive",
  note: "bg-muted text-muted-foreground",
};

/** stageLabel names a step; L3 is a persona for the global store and a doctrine for a project's. */
function stageLabel(a: Activity): string {
  switch (a.stage) {
    case "extract":
      return "L1 extract";
    case "scenes":
      return "L2 scenes";
    case "persona":
      return a.scope === "project" || (!a.scope && /doctrine/.test(a.detail ?? "")) ? "L3 doctrine" : "L3 persona";
    default:
      return a.stage;
  }
}

/** ActivityItem is one step of the learner, linked both ways: to the session it
 * learned from, to the memories it wrote, and to the scenes or persona it
 * rewrote, with the project it was about. */
export function ActivityItem({ a, compact, stores }: { a: Activity; compact?: boolean; stores?: Record<string, StoreInfo> }) {
  const [head, ...rest] = (a.error || a.detail || "").split("\n");
  const store = a.dir ? stores?.[a.dir] : undefined;
  const where = store ? (store.scope === "global" ? "global" : store.name) : a.scope === "global" ? "global" : "";
  return (
    <div className={cn("flex gap-3 rounded-lg px-2 py-1.5 text-sm hover:bg-muted/40", !compact && "border-b last:border-b-0")}>
      <span className={cn("mt-0.5 h-fit shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium whitespace-nowrap", stageStyle[a.stage] ?? stageStyle.note)}>
        {stageLabel(a)}
      </span>
      <div className="min-w-0 flex-1">
        <div className={cn("break-words", a.error && "text-destructive", compact && "truncate")}>
          {where && <span className="mr-1.5 font-medium">{where}:</span>}
          {head}
        </div>
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
          {a.dir && (a.stage === "scenes" || a.stage === "persona") && (
            <Link className="hover:text-foreground hover:underline" href={`/memory?tab=scenes&dir=${encodeURIComponent(a.dir)}`}>
              {a.stage === "persona" ? "read it, and what changed →" : "open the scenes →"}
            </Link>
          )}
        </div>
      </div>
    </div>
  );
}
