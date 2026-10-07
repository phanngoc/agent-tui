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

/** firstLine is the first line of some markdown with its marks taken off. */
export function firstLine(s?: string) {
  return s?.split("\n").find((l) => l.trim())?.replace(/[*#`]/g, "").trim() ?? "";
}

/** agentTask is what an agent was asked to do, in a line. */
export const agentTask = (a: SubAgent) => a.description || firstLine(a.prompt) || "sub-agent";

/** agentNow is what an agent is doing now — or, once done, how it ended. */
export function agentNow(a: SubAgent) {
  if (isRunning(a)) return a.activity || firstLine(a.summary) || (a.state === "starting" ? "starting…" : "working…");
  return firstLine(a.summary) || a.state;
}

/**
 * SubAgentCard is one sub-agent, folded the way Claude Code folds it: its
 * task and what it has used, then what it is doing now and, while it works,
 * the last thing it said. Agents under it still at work show folded beneath
 * it. A click opens it on its prompt, every call it made, and its report.
 */
export function SubAgentCard({ a, depth = 0, flat = false }: { a: SubAgent; depth?: number; flat?: boolean }) {
  const running = isRunning(a);
  const [open, setOpen] = React.useState(false);
  const now = useNow(running);
  const stats = agentStats(a, now);
  const calls = a.calls ?? [];
  const line = agentNow(a);
  const said = running ? firstLine(a.summary) : "";
  const busy = calls.filter((c): c is ToolCall & { agent: SubAgent } => !!c.agent && isRunning(c.agent));
  return (
    <div className={cn(!flat && "rounded-lg border bg-card/50", !flat && running && "border-sky-500/40", depth > 0 && "bg-transparent")}>
      <button className="flex w-full min-w-0 items-center gap-2 px-2.5 pt-1.5 pb-0.5 text-left text-xs" onClick={() => setOpen(!open)}>
        <ChevronRightIcon className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")} />
        <AgentStateIcon a={a} />
        <AgentType type={a.type} />
        <span className="min-w-0 flex-1 truncate font-medium">{agentTask(a)}</span>
        {a.background && <span className="hidden text-[10px] text-muted-foreground sm:inline">background</span>}
        <span className="shrink-0 tabular-nums text-muted-foreground">{stats}</span>
      </button>
      <div className="space-y-0.5 pr-2.5 pb-1.5 pl-[2.6rem] text-xs">
        <div className={cn("flex min-w-0 gap-1.5", running ? "text-sky-700 dark:text-sky-300" : a.state === "failed" ? "text-destructive" : "text-muted-foreground")}>
          <span className="text-muted-foreground">⎿</span>
          <span className="truncate">{line}</span>
        </div>
        {said && said !== line && <div className="truncate pl-4 text-muted-foreground italic">{said}</div>}
      </div>
      {!open && busy.length > 0 && (
        <div className="space-y-1 pr-2 pb-1.5 pl-6">
          {busy.map((c) => (
            <SubAgentCard key={c.id} a={c.agent} depth={depth + 1} flat />
          ))}
        </div>
      )}
      {open && (
        <div className="space-y-2 border-t px-2.5 py-2">
          {a.prompt && (
            <details className="text-xs">
              <summary className="cursor-pointer text-muted-foreground">prompt</summary>
              <Pre max="max-h-48" className="mt-1 text-[11px]">
                {a.prompt}
              </Pre>
            </details>
          )}
          <div className="space-y-1">
            {calls.map((c) =>
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
    </div>
  );
}

/**
 * AgentGroup is agents started side by side, drawn as one block with a row
 * each, as Claude Code draws them: six at work take six rows, not six cards
 * of calls.
 */
export function AgentGroup({ calls }: { calls: (ToolCall & { agent: SubAgent })[] }) {
  let running = 0,
    failed = 0,
    tokens = 0;
  for (const c of calls) {
    if (isRunning(c.agent)) running++;
    else if (c.agent.state === "failed") failed++;
    tokens += c.agent.tokens ?? 0;
  }
  const head =
    running === 0
      ? `${calls.length} agents finished`
      : running < calls.length
        ? `Running ${running} of ${calls.length} agents`
        : `Running ${calls.length} agents`;
  return (
    <div className={cn("rounded-lg border bg-card/50", running > 0 && "border-sky-500/40")}>
      <div className="flex items-center gap-2 border-b px-2.5 py-1.5 text-xs">
        {running > 0 ? (
          <LoaderCircleIcon className="size-3.5 shrink-0 animate-spin text-sky-500" />
        ) : (
          <CheckCircle2Icon className="size-3.5 shrink-0 text-emerald-600" />
        )}
        <span className="font-medium">{head}</span>
        {failed > 0 && <span className="text-destructive">· {failed} failed</span>}
        {tokens > 0 && <span className="text-muted-foreground">· {tokensShort(tokens)} tokens</span>}
      </div>
      <div className="divide-y">
        {calls.map((c) => (
          <SubAgentCard key={c.id} a={c.agent} flat />
        ))}
      </div>
    </div>
  );
}
