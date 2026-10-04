"use client";

import * as React from "react";
import Link from "next/link";
import { ChevronRightIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Message, Trace } from "@/lib/types";
import { Ago, Empty, Pre } from "@/components/common";
import { Badge } from "@/components/ui/badge";
import { memoryTypeLabel } from "@/lib/format";

/** TraceView is everything one turn was given beyond the transcript: what
 * memory recalled and how well it matched, the skills and MCP tools on offer,
 * and the system text itself. */
export function TraceView({ t, open: initial }: { t: Trace; open?: boolean }) {
  const [showSystem, setShowSystem] = React.useState(false);
  return (
    <div className={cn("space-y-2.5 rounded-xl border p-3", initial && "ring-2 ring-primary/40")}>
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Badge variant="outline" className="font-normal">
          {t.engine || "api"}
        </Badge>
        <Ago at={t.at} />
        <span className="ml-auto">{t.system_chars.toLocaleString()} chars added</span>
      </div>
      {t.prompt && <div className="line-clamp-3 text-sm">“{t.prompt}”</div>}

      <Section title={`Recalled memories (${t.recalled.length})`}>
        {t.recalled.length === 0 && <div className="text-xs text-muted-foreground">nothing matched closely enough</div>}
        {t.recalled.map((h) => (
          <Link key={h.id} href={`/memory?ids=${h.id}`} className="block rounded-lg border p-2 text-xs hover:bg-muted/40">
            <div className="mb-1 flex items-center gap-2">
              <div className="h-1.5 w-16 overflow-hidden rounded-full bg-muted" title={`score ${h.score.toFixed(3)}`}>
                <div className="h-full bg-primary" style={{ width: `${Math.min(100, h.score * 100)}%` }} />
              </div>
              <span className="tabular-nums text-muted-foreground">{h.score.toFixed(2)}</span>
              <span className="text-muted-foreground">
                {h.scope} · {memoryTypeLabel[h.type] ?? h.type}
              </span>
            </div>
            <div>{h.content}</div>
          </Link>
        ))}
      </Section>

      <div className="flex flex-wrap gap-1.5 text-xs">
        <Chip on={t.persona}>user persona</Chip>
        <Chip on={t.doctrine}>project doctrine</Chip>
        <Chip on={t.standing > 0}>{t.standing} standing rules</Chip>
        <Chip on={t.learning}>learning {t.learning ? "on" : "off"}</Chip>
      </div>

      <Section title={`Skills listed (${t.skills.length})`}>
        <div className="flex flex-wrap gap-1">
          {t.skills.length === 0 && <span className="text-xs text-muted-foreground">none</span>}
          {t.skills.map((s) => (
            <Link key={s} href={`/skills?name=${s}`}>
              <Badge variant="secondary" className="font-normal hover:bg-secondary/70">
                {s}
              </Badge>
            </Link>
          ))}
        </div>
      </Section>

      <Section title={`MCP servers (${t.mcp.length})`}>
        <div className="flex flex-wrap gap-1">
          {t.mcp.length === 0 && <span className="text-xs text-muted-foreground">none</span>}
          {t.mcp.map((s) => (
            <Link key={s} href={`/mcp?name=${s}`}>
              <Badge variant={t.mcp_errors?.includes(s) ? "destructive" : "outline"} className="font-normal">
                {s}
                {t.mcp_errors?.includes(s) && " · failed"}
              </Badge>
            </Link>
          ))}
        </div>
      </Section>

      <Section title={`Extra tools (${t.tools.length})`}>
        <div className="font-mono text-[11px] text-muted-foreground">{t.tools.join(", ") || "none"}</div>
      </Section>

      <button className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground" onClick={() => setShowSystem(!showSystem)}>
        <ChevronRightIcon className={cn("size-3 transition-transform", showSystem && "rotate-90")} />
        System text added to this turn
      </button>
      {showSystem && <Pre max="max-h-[28rem]">{t.system || "(nothing)"}</Pre>}
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{title}</div>
      {children}
    </div>
  );
}

function Chip({ on, children }: { on: boolean; children: React.ReactNode }) {
  return <span className={cn("rounded-full border px-2 py-0.5", on ? "border-primary/40 text-foreground" : "text-muted-foreground line-through opacity-70")}>{children}</span>;
}

export function TracePanel({ traces, messages, focus }: { traces: Trace[]; messages: Message[]; focus: number | null }) {
  const focusPrompt = focus !== null ? (messages[focus]?.text ?? "").slice(0, 400) : null;
  const ordered = [...traces].reverse();
  const ref = React.useRef<HTMLDivElement>(null);
  React.useEffect(() => {
    ref.current?.querySelector("[data-focus='1']")?.scrollIntoView({ block: "start", behavior: "smooth" });
  }, [focus]);
  if (traces.length === 0) {
    return (
      <Empty title="No trace yet">
        Each turn records what it was given — recalled memories, skills, MCP tools and the system text. Turns run before this version have none.
      </Empty>
    );
  }
  return (
    <div ref={ref} className="space-y-3">
      <p className="text-xs text-muted-foreground">Newest turn first. Hover a prompt in the conversation and pick “what the agent was given” to jump here.</p>
      {ordered.map((t, i) => {
        const hit = focusPrompt !== null && t.prompt && focusPrompt.startsWith(t.prompt.replace(/…$/, ""));
        return (
          <div key={i} data-focus={hit ? "1" : "0"}>
            <TraceView t={t} open={!!hit} />
          </div>
        );
      })}
    </div>
  );
}
