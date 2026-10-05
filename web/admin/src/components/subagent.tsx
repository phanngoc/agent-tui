"use client";

import * as React from "react";
import { ChevronRightIcon, CheckCircle2Icon, XCircleIcon, SquareIcon, LoaderCircleIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import type { SubAgent, ToolCall } from "@/lib/types";
import { toolSummary } from "@/lib/format";
import { Markdown } from "@/components/markdown";
import { Pre } from "@/components/common";

// Sub-agents, the way Claude Code shows its Agent calls: the type and the
// task, then what it is doing now with what it has used — tool uses, tokens,
// time — and its calls; once done, its report. Agents it started sit under it.

const typeColor: Record<string, string> = {
  Explore: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  Plan: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  "general-purpose": "bg-violet-500/15 text-violet-700 dark:text-violet-300",
};

export function AgentType({ type }: { type?: string }) {
  const t = type || "agent";
  return <span className={cn("rounded px-1.5 py-0.5 font-mono text-[11px] font-medium", typeColor[t] ?? "bg-muted text-foreground")}>{t}</span>;
}

export const isRunning = (a?: SubAgent) => !!a && (a.state === "" || a.state === "starting" || a.state === "running");

export function AgentStateIcon({ a, className }: { a: SubAgent; className?: string }) {
  if (isRunning(a)) return <LoaderCircleIcon className={cn("size-3.5 shrink-0 animate-spin text-sky-500", className)} />;
  if (a.state === "failed") return <XCircleIcon className={cn("size-3.5 shrink-0 text-destructive", className)} />;
  if (a.state === "stopped") return <SquareIcon className={cn("size-3.5 shrink-0 text-muted-foreground", className)} />;
  return <CheckCircle2Icon className={cn("size-3.5 shrink-0 text-emerald-600", className)} />;
}

/** useNow ticks while something is running, for elapsed times. */
export function useNow(active: boolean, every = 1000) {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(Date.now()), every);
    return () => clearInterval(t);
  }, [active, every]);
  return now;
}

function tokensShort(n?: number) {
  if (!n) return "";
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}k`;
  return String(n);
}

function secs(ms: number) {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  return m < 60 ? `${m}m ${s % 60}s` : `${Math.floor(m / 60)}h ${m % 60}m`;
}

/** agentStats is "4 tool uses · 21.2k tokens · 9s". */
export function agentStats(a: SubAgent, now: number) {
  const parts: string[] = [];
  const uses = a.tool_uses || a.calls?.length || 0;
  if (uses) parts.push(`${uses} tool use${uses === 1 ? "" : "s"}`);
  if (a.tokens) parts.push(`${tokensShort(a.tokens)} tokens`);
  let ms = (a.duration ?? 0) / 1e6;
  if (isRunning(a) && a.started && !a.started.startsWith("0001")) ms = Math.max(ms, now - new Date(a.started).getTime());
  if (ms >= 1000) parts.push(secs(ms));
  return parts.join(" · ");
}

/** countAgents walks a tree: how many agents, and how many still running. */
export function countAgents(a: SubAgent): { all: number; running: number; tokens: number } {
  let all = 1,
    running = isRunning(a) ? 1 : 0,
    tokens = a.tokens ?? 0;
  for (const c of a.calls ?? []) {
    if (c.agent) {
      const n = countAgents(c.agent);
      all += n.all;
      running += n.running;
      tokens += n.tokens;
    }
  }
  return { all, running, tokens };
}

function CallLine({ c }: { c: ToolCall }) {
  const [open, setOpen] = React.useState(false);
  return (
    <div>
      <button className="flex w-full min-w-0 items-center gap-1.5 text-left text-[11px]" onClick={() => setOpen(!open)}>
        {!c.done ? (
          <LoaderCircleIcon className="size-3 shrink-0 animate-spin text-sky-500" />
        ) : c.is_error ? (
          <XCircleIcon className="size-3 shrink-0 text-destructive" />
        ) : (
          <CheckCircle2Icon className="size-3 shrink-0 text-emerald-600/70" />
        )}
        <span className="shrink-0 font-mono font-medium">{c.name}</span>
        <span className="min-w-0 truncate font-mono text-muted-foreground">{toolSummary(c.input)}</span>
      </button>
      {open && c.result && (
        <Pre max="max-h-48" className="mt-1 text-[11px]">
          {c.result}
        </Pre>
      )}
    </div>
  );
}

/**
 * SubAgentCard is one sub-agent and the agents under it. Running, it is open
 * on its activity and latest calls; finished, it folds to a line and its
 * report, and opens on a click.
 */
export function SubAgentCard({ a, depth = 0 }: { a: SubAgent; depth?: number }) {
  const running = isRunning(a);
  const [open, setOpen] = React.useState<boolean | null>(null);
  const shown = open ?? running;
  const now = useNow(running);
  const stats = agentStats(a, now);
  const calls = a.calls ?? [];
  const recent = shown ? (open ? calls : calls.slice(-4)) : [];
  return (
    <div className={cn("rounded-lg border bg-card/50", running && "border-sky-500/40", depth > 0 && "bg-transparent")}>
      <button className="flex w-full min-w-0 items-center gap-2 px-2.5 py-1.5 text-left text-xs" onClick={() => setOpen(!shown)}>
        <ChevronRightIcon className={cn("size-3.5 shrink-0 transition-transform", shown && "rotate-90")} />
        <AgentStateIcon a={a} />
        <AgentType type={a.type} />
        <span className="min-w-0 flex-1 truncate font-medium">{a.description || "sub-agent"}</span>
        {a.background && <span className="hidden text-[10px] text-muted-foreground sm:inline">background</span>}
        <span className="shrink-0 tabular-nums text-muted-foreground">{stats}</span>
      </button>
      {running && (
        <div className="flex items-center gap-1.5 border-t px-2.5 py-1 text-xs text-sky-700 dark:text-sky-300">
          <span className="text-muted-foreground">⎿</span>
          <span className="truncate">{a.activity || (a.state === "starting" ? "starting…" : "working…")}</span>
        </div>
      )}
      {shown && (
        <div className="space-y-2 border-t px-2.5 py-2">
          {open && a.prompt && (
            <details className="text-xs">
              <summary className="cursor-pointer text-muted-foreground">prompt</summary>
              <Pre max="max-h-48" className="mt-1 text-[11px]">
                {a.prompt}
              </Pre>
            </details>
          )}
          {calls.length > recent.length && <div className="text-[11px] text-muted-foreground">… {calls.length - recent.length} earlier calls</div>}
          <div className="space-y-1">
            {recent.map((c) =>
              c.agent ? (
                <div key={c.id} className="border-l-2 border-muted pl-2">
                  <SubAgentCard a={c.agent} depth={depth + 1} />
                </div>
              ) : (
                <CallLine key={c.id} c={c} />
              ),
            )}
          </div>
          {!running && a.summary && (
            <div className="rounded-md bg-muted/40 px-2.5 py-1.5 text-xs">
              <Markdown text={a.summary} />
            </div>
          )}
        </div>
      )}
      {!shown && !running && a.summary && (
        <div className="flex gap-1.5 border-t px-2.5 py-1 text-xs text-muted-foreground">
          <span>⎿</span>
          <span className="truncate">{a.summary.split("\n").find((l) => l.trim())?.replace(/[*#`]/g, "")}</span>
        </div>
      )}
    </div>
  );
}
