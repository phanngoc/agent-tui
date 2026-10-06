"use client";

import * as React from "react";
import { ChevronRightIcon, WrenchIcon, BrainCircuitIcon, UserIcon, BotIcon, TerminalSquareIcon, XCircleIcon, ShieldAlertIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Live, Message, ToolCall } from "@/lib/types";
import { Pre } from "@/components/common";
import { Markdown } from "@/components/markdown";
import { nanos, pretty, stamp, toolSummary } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { SubAgentCard } from "@/components/subagent";
import { useFileOpener } from "@/lib/file-opener";

export function ToolRow({ call, output, running }: { call: ToolCall; output?: string; running?: boolean }) {
  if (call.agent) return <SubAgentCard a={call.agent} />;
  return <PlainToolRow call={call} output={output} running={running} />;
}

/** filePathOf is the file a tool call worked on, when it names one. */
function filePathOf(input: unknown): string | undefined {
  if (!input || typeof input !== "object") return undefined;
  const o = input as Record<string, unknown>;
  for (const k of ["file_path", "path", "notebook_path", "filePath"]) {
    const v = o[k];
    if (typeof v === "string" && v.trim() && /\.[A-Za-z0-9]{1,8}$/.test(v)) return v;
  }
  return undefined;
}

function PlainToolRow({ call, output, running }: { call: ToolCall; output?: string; running?: boolean }) {
  const [open, setOpen] = React.useState(false);
  const openFile = useFileOpener();
  const file = openFile ? filePathOf(call.input) : undefined;
  const isMcp = call.name.startsWith("mcp__");
  const isKit = ["skill", "memory_search", "memory_read", "memory_save"].includes(call.name);
  return (
    <div className="rounded-lg border bg-card/50">
      <button className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs" onClick={() => setOpen(!open)}>
        <ChevronRightIcon className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")} />
        {call.is_error ? (
          <XCircleIcon className="size-3.5 shrink-0 text-destructive" />
        ) : (
          <WrenchIcon className={cn("size-3.5 shrink-0", running ? "animate-pulse text-sky-500" : "text-muted-foreground")} />
        )}
        <span className={cn("font-mono font-medium", isMcp && "text-violet-600 dark:text-violet-400", isKit && "text-emerald-600 dark:text-emerald-400")}>{call.name}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground">{toolSummary(call.input)}</span>
        {call.denied && <span className="text-amber-600">denied</span>}
        {running ? <span className="text-sky-600">running…</span> : <span className="text-muted-foreground">{nanos(call.elapsed)}</span>}
        {file && (
          <span
            role="link"
            tabIndex={0}
            title={`Open ${file} in the project explorer`}
            onClick={(e) => {
              e.stopPropagation();
              openFile?.(file);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.stopPropagation();
                openFile?.(file);
              }
            }}
            className="rounded px-1 text-sky-700 hover:bg-sky-500/10 dark:text-sky-300"
          >
            open
          </span>
        )}
      </button>
      {(open || (running && output)) && (
        <div className="space-y-2 border-t px-2.5 py-2">
          {open && (
            <div>
              <div className="mb-1 text-[11px] font-medium uppercase text-muted-foreground">input</div>
              <Pre max="max-h-60">{pretty(call.input)}</Pre>
            </div>
          )}
          {(call.result || output) && (
            <div>
              <div className="mb-1 text-[11px] font-medium uppercase text-muted-foreground">{running ? "live output" : "result"}</div>
              <Pre max="max-h-80" className={call.is_error ? "border-destructive/40" : ""}>
                {running ? output : call.result}
              </Pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Thinking({ text, live }: { text: string; live?: boolean }) {
  const [open, setOpen] = React.useState(false);
  return (
    <div className="text-xs text-muted-foreground">
      <button className="flex items-center gap-1.5 hover:text-foreground" onClick={() => setOpen(!open)}>
        <BrainCircuitIcon className={cn("size-3.5", live && "animate-pulse")} />
        {live ? "thinking…" : "thought"} <span className="tabular-nums">({text.length} chars)</span>
        <ChevronRightIcon className={cn("size-3 transition-transform", open && "rotate-90")} />
      </button>
      {open && <Pre className="mt-1">{text}</Pre>}
    </div>
  );
}

/** Body renders a message as Markdown, or as the text it was written in. */
function Body({ text, raw }: { text: string; raw: boolean }) {
  if (raw) return <Pre max="max-h-[40rem]">{text}</Pre>;
  return <Markdown text={text} />;
}

function RawToggle({ raw, setRaw }: { raw: boolean; setRaw: (r: boolean) => void }) {
  return (
    <button className="opacity-0 hover:text-foreground hover:underline group-hover:opacity-100" onClick={() => setRaw(!raw)}>
      {raw ? "rendered" : "raw"}
    </button>
  );
}

export function MessageView({ m, index, onTrace }: { m: Message; index: number; onTrace?: (index: number) => void }) {
  const [raw, setRaw] = React.useState(false);
  if (m.role === "user") {
    return (
      <div className="group flex gap-3" id={`m${index}`}>
        <div className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground">
          <UserIcon className="size-3.5" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
            <span className="font-medium text-foreground">you</span>
            <span title={stamp(m.at)}>{new Date(m.at).toLocaleTimeString()}</span>
            <span className="opacity-0 group-hover:opacity-100">#{index}</span>
            {!m.shell && <RawToggle raw={raw} setRaw={setRaw} />}
            {onTrace && (
              <button className="opacity-0 hover:text-foreground hover:underline group-hover:opacity-100" onClick={() => onTrace(index)}>
                what the agent was given →
              </button>
            )}
          </div>
          {m.shell ? (
            <div className="space-y-1">
              <div className="flex items-center gap-1.5 font-mono text-xs">
                <TerminalSquareIcon className="size-3.5" />$ {m.shell.command} <span className="text-muted-foreground">exit {m.shell.exit}</span>
              </div>
              {m.shell.output && <Pre>{m.shell.output}</Pre>}
            </div>
          ) : (
            <div className="rounded-xl bg-muted/60 px-3 py-2">
              <Body text={m.text ?? ""} raw={raw} />
            </div>
          )}
          {m.files && m.files.length > 0 && <div className="mt-1 text-xs text-muted-foreground">attached: {m.files.map((f) => f.path).join(", ")}</div>}
        </div>
      </div>
    );
  }
  return (
    <div className="group flex gap-3" id={`m${index}`}>
      <div className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border bg-background">
        <BotIcon className="size-3.5" />
      </div>
      <div className="min-w-0 flex-1 space-y-2">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">agent</span>
          <span title={stamp(m.at)}>{new Date(m.at).toLocaleTimeString()}</span>
          <span className="opacity-0 group-hover:opacity-100">#{index}</span>
          {m.text && <RawToggle raw={raw} setRaw={setRaw} />}
        </div>
        {m.thinking && <Thinking text={m.thinking} />}
        {m.text && <Body text={m.text} raw={raw} />}
        {m.tools && m.tools.length > 0 && (
          <div className="space-y-1">
            {m.tools.map((t) => (
              <ToolRow key={t.id} call={t} />
            ))}
          </div>
        )}
        {m.err && <div className="rounded-lg border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">{m.err}</div>}
      </div>
    </div>
  );
}

/** LiveTail is the turn in flight: streamed text, running tools, and the
 * questions waiting on someone — answerable from here as from the terminal. */
export function LiveTail({
  live,
  onApprove,
  onChoose,
}: {
  live?: Live;
  onApprove: (id: string, verdict: "allow" | "allow_all" | "deny") => void;
  onChoose: (id: string, index: number) => void;
}) {
  if (!live || (!live.busy && !live.error)) return null;
  if (!live.busy && live.error) {
    return <div className="rounded-lg border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">turn ended: {live.error}</div>;
  }
  return (
    <div className="flex gap-3">
      <div className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border bg-background">
        <BotIcon className="size-3.5 animate-pulse" />
      </div>
      <div className="min-w-0 flex-1 space-y-2">
        <div className="flex items-center gap-2 text-xs text-sky-600 dark:text-sky-400">
          <span className="relative flex size-2">
            <span className="absolute inline-flex size-full animate-ping rounded-full bg-sky-400 opacity-60" />
            <span className="relative inline-flex size-2 rounded-full bg-sky-500" />
          </span>
          {live.status || "working"}
        </div>
        {live.thinking && <Thinking text={live.thinking} live />}
        {live.partial && <Markdown text={live.partial} streaming />}
        {Object.values(live.running).map((c) => (
          <ToolRow key={c.id} call={c} output={live.output[c.id]} running />
        ))}
        {Object.values(live.approvals).map((a) => (
          <div key={a.id} className="space-y-2 rounded-xl border border-amber-500/50 bg-amber-500/5 p-3">
            <div className="flex items-center gap-2 text-sm font-medium">
              <ShieldAlertIcon className="size-4 text-amber-600" /> Approval needed: <span className="font-mono">{a.call.name}</span>
            </div>
            <div className="text-xs text-muted-foreground">{a.reason}</div>
            <Pre max="max-h-48">{pretty(a.call.input)}</Pre>
            <div className="flex gap-2">
              <Button size="sm" onClick={() => onApprove(a.id, "allow")}>
                Allow
              </Button>
              <Button size="sm" variant="outline" onClick={() => onApprove(a.id, "allow_all")}>
                Allow rest of turn
              </Button>
              <Button size="sm" variant="destructive" onClick={() => onApprove(a.id, "deny")}>
                Deny
              </Button>
            </div>
          </div>
        ))}
        {Object.values(live.choices).map((c) => (
          <div key={c.id} className="space-y-2 rounded-xl border border-sky-500/50 bg-sky-500/5 p-3">
            <div className="text-sm font-medium">{c.question}</div>
            <div className="flex flex-col gap-1.5">
              {c.options.map((o, i) => (
                <button key={i} className="rounded-lg border bg-background px-3 py-2 text-left text-sm hover:bg-muted" onClick={() => onChoose(c.id, i)}>
                  <div className="font-medium">{o.label}</div>
                  {o.detail && <div className="text-xs text-muted-foreground">{o.detail}</div>}
                </button>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
