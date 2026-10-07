"use client";

import * as React from "react";
import Link from "next/link";
import { BotIcon, LoaderCircleIcon, WrenchIcon } from "lucide-react";
import { useFetch } from "@/lib/hooks";
import { useGateway, useVersion } from "@/lib/store";
import type { Live, SubAgent, Summary, ToolCall } from "@/lib/types";
import { baseName, toolSummary } from "@/lib/format";
import { Empty, PageHeader } from "@/components/common";
import { OwnerBadge } from "@/components/owner-badge";
import { AgentStateIcon, AgentType, agentNow, agentStats, agentTask, countAgents, firstLine, isRunning, useNow } from "@/components/subagent";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

// The agent map: every agent at work, across sessions, and the sub-agents each
// started — the way Claude Code follows its Agent calls, for every terminal
// and the gateway at once. It is drawn from the live state the gateway keeps
// and the event stream, so it moves as they work.

type Lane = { live: Live; summary?: Summary; agents: [string, SubAgent][] };

export default function AgentsPage() {
  const live = useGateway((s) => s.live);
  const summaries = useGateway((s) => s.summaries);
  const v = useVersion("sessions");
  const { data: list } = useFetch<Summary[]>("/api/sessions", [v]);
  const [showDone, setShowDone] = React.useState(true);

  const byId = React.useMemo(() => {
    const m: Record<string, Summary> = {};
    for (const s of list ?? []) m[s.id] = s;
    return { ...m, ...summaries };
  }, [list, summaries]);

  const lanes: Lane[] = Object.values(live)
    .map((l) => ({
      live: l,
      summary: byId[l.session],
      agents: Object.entries(l.agents ?? {}).filter(([, a]) => showDone || isRunning(a) || hasRunning(a)),
    }))
    .filter((x) => x.live.busy || x.agents.length > 0)
    .sort((a, b) => Number(b.live.busy) - Number(a.live.busy) || (b.live.started ?? "").localeCompare(a.live.started ?? ""));

  let agents = 0,
    running = 0,
    tokens = 0;
  for (const lane of lanes)
    for (const [, a] of lane.agents) {
      const n = countAgents(a);
      agents += n.all;
      running += n.running;
      tokens += n.tokens;
    }
  const busy = lanes.filter((l) => l.live.busy).length;
  const now = useNow(busy > 0 || running > 0);

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="Agents"
        description="Every agent at work right now, in every terminal and the gateway, and the sub-agents each one started — their task, what they are doing this second, and what they have used. A turn's agents stay here until its session starts another."
        actions={
          <label className="flex items-center gap-2 text-sm text-muted-foreground">
            <input type="checkbox" checked={showDone} onChange={(e) => setShowDone(e.target.checked)} className="accent-primary" />
            show finished
          </label>
        }
      />
      <div className="flex flex-wrap gap-2 border-b px-6 py-3 text-sm">
        <Stat label="sessions working" value={busy} live={busy > 0} />
        <Stat label="sub-agents running" value={running} live={running > 0} />
        <Stat label="sub-agents this turn" value={agents} />
        <Stat label="sub-agent tokens" value={tokens >= 1000 ? `${(tokens / 1000).toFixed(1)}k` : tokens} />
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-6">
        {lanes.length === 0 ? (
          <Empty title="No agent is working">
            When a session runs a turn it appears here, with its sub-agents under it as it starts them — Claude Code&apos;s Agent tool (Explore, Plan,
            general-purpose, your own). Start one from a terminal, a chat here, or a schedule.
          </Empty>
        ) : (
          <div className="space-y-5">
            {lanes.map((lane) => (
              <LaneView key={lane.live.session} lane={lane} now={now} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function hasRunning(a: SubAgent): boolean {
  return (a.calls ?? []).some((c) => c.agent && (isRunning(c.agent) || hasRunning(c.agent)));
}

function Stat({ label, value, live }: { label: string; value: React.ReactNode; live?: boolean }) {
  return (
    <div className={cn("flex items-baseline gap-2 rounded-lg border px-3 py-1.5", live && "border-sky-500/40 bg-sky-500/5")}>
      <span className="text-lg font-semibold tabular-nums">{value}</span>
      <span className="text-xs text-muted-foreground">{label}</span>
    </div>
  );
}

function since(iso: string | undefined, now: number) {
  if (!iso || iso.startsWith("0001")) return "";
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

/** LaneView is one session: its main agent, then the tree of agents it started. */
function LaneView({ lane, now }: { lane: Lane; now: number }) {
  const { live, summary, agents } = lane;
  const tools = Object.values(live.running ?? {}).filter((c) => !c.agent && c.name !== "Agent" && c.name !== "Task");
  return (
    <div className="rounded-2xl border bg-card">
      <div className="flex flex-wrap items-center gap-2 border-b px-4 py-3">
        {live.busy ? <LoaderCircleIcon className="size-4 animate-spin text-sky-500" /> : <BotIcon className="size-4 text-muted-foreground" />}
        <Link href={`/sessions?id=${live.session}`} className="min-w-0 truncate font-medium hover:underline">
          {summary?.title || "(untitled)"}
        </Link>
        <span className="text-xs text-muted-foreground">{summary ? baseName(summary.root) : ""}</span>
        {live.engine && (
          <Badge variant="outline" className="font-normal">
            {live.engine}
          </Badge>
        )}
        {summary?.model && (
          <Badge variant="outline" className="font-normal">
            {summary.model}
          </Badge>
        )}
        <OwnerBadge owner={live.owner || summary?.owner} />
        <span className="ml-auto text-xs tabular-nums text-muted-foreground">{live.busy ? since(live.started, now) : "turn finished"}</span>
      </div>
      <div className="px-4 py-3">
        {/* The main agent's own line: what it is doing while its agents work. */}
        <div className="flex min-w-0 items-center gap-2 text-sm">
          <span className="rounded bg-foreground/90 px-1.5 py-0.5 font-mono text-[11px] font-medium text-background">main</span>
          <span className="min-w-0 truncate text-muted-foreground">
            {live.busy ? live.status || "working" : live.error ? `failed: ${live.error}` : "idle"}
            {live.prompt ? ` — “${live.prompt.slice(0, 120)}”` : ""}
          </span>
        </div>
        {tools.length > 0 && (
          <div className="mt-1.5 space-y-0.5 pl-12">
            {tools.map((c) => (
              <div key={c.id} className="flex min-w-0 items-center gap-1.5 text-xs">
                <WrenchIcon className="size-3 shrink-0 animate-pulse text-sky-500" />
                <span className="font-mono font-medium">{c.name}</span>
                <span className="truncate font-mono text-muted-foreground">{toolSummary(c.input)}</span>
              </div>
            ))}
          </div>
        )}
        {agents.length > 0 && (
          <div className="mt-2 ml-3 border-l-2 border-dashed border-muted-foreground/25 pl-0">
            {agents.map(([id, a]) => (
              <Node key={id} a={a} now={now} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

/** Node is one agent on the map, with a branch to it and its own children. */
function Node({ a, now }: { a: SubAgent; now: number }) {
  const [open, setOpen] = React.useState(false);
  const running = isRunning(a);
  const calls = a.calls ?? [];
  const children = calls.filter((c): c is ToolCall & { agent: SubAgent } => !!c.agent);
  const plain = calls.filter((c) => !c.agent);
  return (
    <div className="relative pt-2 pl-6">
      <span className="absolute top-[1.35rem] left-0 h-px w-5 bg-muted-foreground/25" />
      <div className={cn("rounded-xl border px-3 py-2", running ? "border-sky-500/50 bg-sky-500/5" : "bg-background")}>
        <button className="flex w-full min-w-0 items-center gap-2 text-left text-sm" onClick={() => setOpen(!open)}>
          <AgentStateIcon a={a} />
          <AgentType type={a.type} />
          <span className="min-w-0 flex-1 truncate font-medium">{agentTask(a)}</span>
          <span className="shrink-0 text-xs tabular-nums text-muted-foreground">{agentStats(a, now)}</span>
        </button>
        <div className="mt-1 flex min-w-0 items-center gap-2 pl-6 text-xs">
          <span className={cn("truncate", running ? "text-sky-700 dark:text-sky-300" : "text-muted-foreground")}>⎿ {agentNow(a)}</span>
        </div>
        {running && firstLine(a.summary) && firstLine(a.summary) !== agentNow(a) && (
          <div className="truncate pl-10 text-xs text-muted-foreground italic">{firstLine(a.summary)}</div>
        )}
        {/* The trail of its calls, newest last: a glance says how it is going. */}
        {plain.length > 0 && (
          <div className="mt-1.5 flex flex-wrap gap-1 pl-6">
            {plain.slice(-24).map((c) => (
              <span
                key={c.id}
                title={`${c.name} ${toolSummary(c.input)}`}
                className={cn(
                  "rounded px-1 py-px font-mono text-[10px]",
                  !c.done ? "animate-pulse bg-sky-500/20 text-sky-700 dark:text-sky-300" : c.is_error ? "bg-destructive/15 text-destructive" : "bg-muted text-muted-foreground",
                )}
              >
                {c.name}
              </span>
            ))}
          </div>
        )}
        {open && (
          <div className="mt-2 space-y-1 border-t pt-2 pl-6 text-xs">
            {a.prompt && (
              <div className="mb-2 rounded-md bg-muted/40 px-2 py-1.5 whitespace-pre-wrap text-muted-foreground">{a.prompt.slice(0, 1200)}</div>
            )}
            {plain.map((c) => (
              <div key={c.id} className="flex min-w-0 gap-1.5">
                <span className="shrink-0 font-mono font-medium">{c.name}</span>
                <span className="truncate font-mono text-muted-foreground">{toolSummary(c.input)}</span>
              </div>
            ))}
          </div>
        )}
      </div>
      {children.length > 0 && (
        <div className="ml-3 border-l-2 border-dashed border-muted-foreground/25">
          {children.map((c) => (
            <Node key={c.id} a={c.agent} now={now} />
          ))}
        </div>
      )}
    </div>
  );
}
