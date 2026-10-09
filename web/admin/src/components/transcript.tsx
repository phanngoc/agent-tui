"use client";

import * as React from "react";
import { attachmentURL } from "@/components/attachments";
import { useRouter } from "next/navigation";
import { ChevronRightIcon, BrainCircuitIcon, UserIcon, BotIcon, TerminalSquareIcon, ShieldAlertIcon, LibraryIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Live, Message, SubAgent, ToolCall } from "@/lib/types";
import { Pre } from "@/components/common";
import { Markdown } from "@/components/markdown";
import { baseName, nanos, pretty, stamp, toolSummary } from "@/lib/format";
import { ImageViewer } from "@/components/image-viewer";
import { Button } from "@/components/ui/button";
import { AgentGroup, SubAgentCard } from "@/components/subagent";
import { filePathOf, useFileOpener } from "@/lib/file-opener";
import { ChapterMark, MessageActions } from "@/components/message-actions";
import { DiffView, changeCounts, fileChange, type FileChange } from "@/components/diff-view";
import { CommandChip, splitCommand } from "@/components/commands";

export function ToolRow({ call, output, running }: { call: ToolCall; output?: string; running?: boolean }) {
  if (call.agent) return <SubAgentCard a={call.agent} />;
  return <PlainToolRow call={call} output={output} running={running} />;
}

type AgentCall = ToolCall & { agent: SubAgent };

/** groupAgents gathers Agent calls made side by side, so they draw as one block. */
function groupAgents(tools: ToolCall[]): (ToolCall | AgentCall[])[] {
  const out: (ToolCall | AgentCall[])[] = [];
  for (let i = 0; i < tools.length; ) {
    let j = i;
    while (j < tools.length && tools[j].agent) j++;
    if (j - i > 1) {
      out.push(tools.slice(i, j) as AgentCall[]);
      i = j;
    } else {
      out.push(tools[i]);
      i++;
    }
  }
  return out;
}

/** toolLabel is a tool's name as a reader wants it: an MCP tool as
 * "server · tool" rather than mcp__server__tool. */
function toolLabel(name: string): string {
  if (!name.startsWith("mcp__")) return name;
  const [server, ...rest] = name.slice(5).split("__");
  return rest.length ? `${server} · ${rest.join("__")}` : server;
}

/** toolNote is what the agent said a call is for, when it said: Bash's
 * description, a sub-task's description. */
function toolNote(input: unknown): string {
  if (!input || typeof input !== "object") return "";
  const d = (input as Record<string, unknown>).description;
  return typeof d === "string" ? d.trim() : "";
}

/** callGist is the one line a call is shown by. A shell command loses the
 * `cd <dir> &&` it starts with, which says where rather than what, and its
 * first line stands for the rest. */
function callGist(call: ToolCall): string {
  const cmd = call.input && typeof call.input === "object" ? (call.input as Record<string, unknown>).command : undefined;
  if (typeof cmd !== "string") return toolSummary(call.input);
  const rest = cmd.replace(/^\s*cd\s+("[^"]*"|'[^']*'|\S+)\s*(&&|;|\n)\s*/, "");
  return (rest.trim() ? rest : cmd).trim().split("\n")[0].slice(0, 160);
}

/** Dot is a call's state, as Claude Code draws it: green done, red failed,
 * blue running, amber denied. */
function Dot({ call, running }: { call: ToolCall; running?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "shrink-0 select-none text-[10px] leading-none",
        running ? "animate-pulse text-sky-500" : call.is_error ? "text-destructive" : call.denied ? "text-amber-500" : "text-emerald-500",
      )}
    >
      ●
    </span>
  );
}

/** ProjectRoot is the folder of the conversation on show, so paths in it can
 * be shown from there. */
export const ProjectRoot = React.createContext("");

/** AnchorPrefix starts each message's element id (#m12): a second transcript
 * on the page, as the side chat is, takes another so jumps land in the main
 * one. */
export const AnchorPrefix = React.createContext("m");

/** posix is a path with forward slashes, a WSL share (\\wsl.localhost\Distro
 * or \\wsl$\Distro) read as the Linux path it is. */
function posix(p: string): string {
  return p.replace(/\\/g, "/").replace(/^\/\/wsl(\.localhost|\$)\/[^/]+/i, "").replace(/\/+$/, "");
}

/** inProject is a path as seen from the project's folder, when it is in it. */
function inProject(path: string, root: string): string {
  if (!root) return path;
  const p = posix(path);
  const r = posix(root);
  return r && p.toLowerCase().startsWith(r.toLowerCase() + "/") ? p.slice(r.length + 1) : path;
}

/** errorText is a failed call's result without the <tool_use_error> tag
 * Claude Code wraps its own refusals in; the engine drops it, sessions saved
 * before that still have it. */
export function errorText(result: string): string {
  const m = /^\s*<tool_use_error>([\s\S]*)<\/tool_use_error>\s*$/.exec(result);
  return m ? m[1].trim() : result;
}

/** errorWhy explains a refusal that reads like a fault of the app: Claude
 * Code's Write only replaces a file it has Read in this conversation, so a
 * file a shell command made is refused, and the agent goes another way. */
function errorWhy(err: string): string | undefined {
  if (/has not been read yet/i.test(err))
    return "Claude Code only overwrites a file it has read in this conversation; one made by a shell command does not count. The agent usually reads it or writes it another way next.";
  if (/modified since read/i.test(err)) return "The file changed after the agent read it, so Claude Code refused to write over the change. The agent reads it again and retries.";
  return undefined;
}

/** CallError is why a call failed, under its line. */
function CallError({ result }: { result: string }) {
  const err = errorText(result);
  const why = errorWhy(err);
  return (
    <div className="ml-6 text-xs">
      <div className="truncate text-destructive" title={err}>
        Error: {err.split("\n")[0]}
      </div>
      {why && <div className="text-muted-foreground">{why}</div>}
    </div>
  );
}

/** EditRow is a call that changed a file, drawn as Claude Code draws it: what
 * it did to which file and by how many lines, then the diff itself, open —
 * the change is the point of the call. A failed, refused or running edit
 * stays shut and says why. */
function EditRow({ call, change, running }: { call: ToolCall; change: FileChange; running?: boolean }) {
  const ok = !running && !call.is_error && !call.denied;
  const [open, setOpen] = React.useState(ok);
  const openFile = useFileOpener();
  const root = React.useContext(ProjectRoot);
  const { added, removed } = React.useMemo(() => changeCounts(change), [change]);
  const lines = change.kind === "write" ? change.hunks[0].new.split("\n").length : 0;
  return (
    <div>
      <button className="flex w-full items-center gap-2 rounded-md px-1.5 py-0.5 text-left text-[13px] hover:bg-muted/60" onClick={() => setOpen(!open)}>
        <Dot call={call} running={running} />
        <span className="shrink-0 font-medium">{change.kind === "write" ? "Write" : "Update"}</span>
        <span
          role={openFile ? "link" : undefined}
          className={cn("min-w-0 truncate font-mono text-xs", openFile && "text-sky-700 hover:underline dark:text-sky-300")}
          title={openFile ? `Open ${change.path} in the project explorer` : change.path}
          onClick={(e) => {
            if (!openFile) return;
            e.stopPropagation();
            openFile(change.path);
          }}
        >
          {inProject(change.path, root)}
        </span>
        <span className="flex-1 shrink-0 text-xs whitespace-nowrap tabular-nums">
          {change.kind === "write" ? (
            <span className="text-muted-foreground">· {lines} lines</span>
          ) : (
            <>
              <span className="text-emerald-600 dark:text-emerald-400">+{added}</span> <span className="text-red-600 dark:text-red-400">−{removed}</span>
            </>
          )}
        </span>
        {call.denied && <span className="text-xs text-amber-600">denied</span>}
        {running && <span className="text-xs text-sky-600">running…</span>}
        <span className="text-muted-foreground"> {open ? "▾" : "▸"}</span>
      </button>
      {open && (
        <div className="mt-1 mb-2 ml-[0.6rem] space-y-2 border-l pl-4">
          <DiffView change={change} />
          {call.is_error && call.result && (
            <Pre max="max-h-40" className="border-destructive/40">
              {errorText(call.result)}
            </Pre>
          )}
        </div>
      )}
      {!open && call.is_error && call.result && <CallError result={call.result} />}
    </div>
  );
}

function PlainToolRow({ call, output, running }: { call: ToolCall; output?: string; running?: boolean }) {
  const change = React.useMemo(() => fileChange(call), [call]);
  if (change) return <EditRow call={call} change={change} running={running} />;
  return <GenericToolRow call={call} output={output} running={running} />;
}

/**
 * wikiLinkOf is where in the wiki a wiki tool's work can be read: the note
 * wiki_add kept (and the pages it is read into), or the page wiki_write wrote.
 */
function wikiLinkOf(call: ToolCall, root: string): string | undefined {
  const name = call.name.replace(/^mcp__agent-tui__/, "");
  const r = call.result ?? "";
  if (call.is_error || !r) return undefined;
  const at = (k: string, v: string) => "/wiki?" + new URLSearchParams({ ...(root ? { root } : {}), [k]: v }).toString();
  if (name === "wiki_add") {
    const m = /raw\/(notes\/[^\s;,]+?\.md)/.exec(r);
    return m ? at("doc", m[1]) : undefined;
  }
  if (name === "wiki_write") {
    const m = /^saved ([^\s.]+(?:\.[^\s.]+)*?)(?:\.\s|\.?$|\s)/u.exec(r);
    return m ? at("page", m[1]) : undefined;
  }
  return undefined;
}

function GenericToolRow({ call, output, running }: { call: ToolCall; output?: string; running?: boolean }) {
  const [open, setOpen] = React.useState(false);
  const root = React.useContext(ProjectRoot);
  const router = useRouter();
  const openFile = useFileOpener();
  const file = openFile ? filePathOf(call.input) : undefined;
  const wiki = running ? undefined : wikiLinkOf(call, root);
  const isMcp = call.name.startsWith("mcp__");
  const isKit = ["skill", "memory_search", "memory_read", "memory_save"].includes(call.name);
  const note = toolNote(call.input);
  return (
    <div>
      <button
        className="flex w-full items-center gap-2 rounded-md px-1.5 py-0.5 text-left text-[13px] hover:bg-muted/60"
        onClick={() => setOpen(!open)}
        title={note || undefined}
      >
        <Dot call={call} running={running} />
        <span className={cn("shrink-0 font-medium", isMcp && "text-violet-600 dark:text-violet-400", isKit && "text-emerald-600 dark:text-emerald-400")}>
          {toolLabel(call.name)}
        </span>
        {note && <span className="min-w-0 shrink truncate">{note}</span>}
        <span className={cn("min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground", note && "basis-1/3 opacity-75")}>{inProject(callGist(call), root)}</span>
        {call.denied && <span className="text-xs text-amber-600">denied</span>}
        {running ? <span className="text-xs text-sky-600">running…</span> : <span className="text-xs text-muted-foreground tabular-nums">{nanos(call.elapsed)}</span>}
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
            className="rounded px-1 text-xs text-sky-700 hover:bg-sky-500/10 dark:text-sky-300"
          >
            open
          </span>
        )}
        {wiki && (
          <span
            role="link"
            tabIndex={0}
            title="Read it in the wiki"
            onClick={(e) => {
              e.stopPropagation();
              router.push(wiki);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.stopPropagation();
                router.push(wiki);
              }
            }}
            className="flex shrink-0 items-center gap-1 rounded px-1 text-xs text-sky-700 hover:bg-sky-500/10 dark:text-sky-300"
          >
            <LibraryIcon className="size-3" /> read in wiki
          </span>
        )}
      </button>
      {!open && !running && call.is_error && call.result && <CallError result={call.result} />}
      {(open || (running && output)) && (
        <div className="mt-1 mb-2 ml-[0.6rem] space-y-2 border-l pl-4">
          {open && note && <div className="text-xs text-muted-foreground">{note}</div>}
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
                {running ? output : call.is_error && call.result ? errorText(call.result) : call.result}
              </Pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/** EXPLORE are the calls that only look around. A run of them says little one
 * by one, so it folds into one line, as Claude Code does: "Read 3 files,
 * searched 2 patterns". */
const EXPLORE: Record<string, [verb: string, noun: string]> = {
  Read: ["read", "file"],
  NotebookRead: ["read", "notebook"],
  Grep: ["searched", "pattern"],
  Glob: ["listed", "pattern"],
  LS: ["listed", "directory"],
  ToolSearch: ["loaded", "tool"],
  WebSearch: ["searched the web for", "query"],
  WebFetch: ["fetched", "page"],
  memory_search: ["searched memory for", "query"],
  memory_read: ["read", "memory note"],
};

const isExplore = (c: ToolCall) => !c.agent && c.name in EXPLORE;

function plural(noun: string, n: number): string {
  if (n === 1) return noun;
  if (/[^aeiou]y$/.test(noun)) return noun.slice(0, -1) + "ies";
  return noun + "s";
}

/** exploreGist is a run of looking-around calls in a few words. */
export function exploreGist(calls: ToolCall[]): string {
  const counts = new Map<string, { verb: string; noun: string; n: number }>();
  for (const c of calls) {
    const [verb, noun] = EXPLORE[c.name];
    const k = verb + "|" + noun;
    const e = counts.get(k) ?? { verb, noun, n: 0 };
    e.n++;
    counts.set(k, e);
  }
  const s = [...counts.values()].map((e) => `${e.verb} ${e.n} ${plural(e.noun, e.n)}`).join(", ");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function ExploreRow({ calls }: { calls: ToolCall[] }) {
  const [open, setOpen] = React.useState(false);
  const failed = calls.filter((c) => c.is_error).length;
  const elapsed = calls.reduce((n, c) => n + (c.elapsed ?? 0), 0);
  return (
    <div>
      <button className="flex w-full items-center gap-2 rounded-md px-1.5 py-0.5 text-left text-[13px] hover:bg-muted/60" onClick={() => setOpen(!open)}>
        <Dot call={{ ...calls[0], is_error: failed === calls.length, denied: false }} />
        <span className="min-w-0 flex-1 truncate">
          <span className="font-medium">{exploreGist(calls)}</span>
          {failed > 0 && failed < calls.length && <span className="text-destructive"> · {failed} failed</span>}
          <span className="text-muted-foreground"> {open ? "▾" : "▸"}</span>
        </span>
        <span className="text-xs text-muted-foreground tabular-nums">{nanos(elapsed)}</span>
      </button>
      {open && (
        <div className="mt-0.5 mb-1 ml-[0.6rem] border-l pl-2">
          {calls.map((c) => (
            <PlainToolRow key={c.id} call={c} />
          ))}
        </div>
      )}
    </div>
  );
}

type StackItem = { kind: "tool"; call: ToolCall } | { kind: "explore"; calls: ToolCall[] } | { kind: "agents"; calls: AgentCall[] };

/** stackItems lays out calls made one after another: side-by-side agents as
 * one block, two or more looking-around calls in a row as one line. */
function stackItems(tools: ToolCall[]): StackItem[] {
  const out: StackItem[] = [];
  for (const g of groupAgents(tools)) {
    if (Array.isArray(g)) {
      out.push({ kind: "agents", calls: g });
      continue;
    }
    const last = out[out.length - 1];
    if (isExplore(g) && last?.kind === "explore") last.calls.push(g);
    else if (isExplore(g) && last?.kind === "tool" && isExplore(last.call)) out[out.length - 1] = { kind: "explore", calls: [last.call, g] };
    else out.push({ kind: "tool", call: g });
  }
  return out;
}

/** ToolStack draws calls made one after another, with nothing said between them. */
function ToolStack({ calls }: { calls: ToolCall[] }) {
  // The agent's steps are how it got there, not what it said: a copy for
  // Slack leaves them out.
  return (
    <div data-copy-skip className="-ml-1.5">
      {stackItems(calls).map((it) =>
        it.kind === "agents" ? (
          <AgentGroup key={it.calls[0].id} calls={it.calls} />
        ) : it.kind === "explore" ? (
          <ExploreRow key={it.calls[0].id} calls={it.calls} />
        ) : (
          <ToolRow key={it.call.id} call={it.call} />
        ),
      )}
    </div>
  );
}

function Thinking({ text, live }: { text: string; live?: boolean }) {
  const [open, setOpen] = React.useState(false);
  return (
    <div data-copy-skip className="text-xs text-muted-foreground">
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

const isImage = (f: { path: string; media?: string }) => (f.media ?? "").startsWith("image/") || /\.(png|jpe?g|gif|webp)$/i.test(f.path);

export function MessageView({ m, index, onTrace, turnEnd }: { m: Message; index: number; onTrace?: (index: number) => void; turnEnd?: boolean }) {
  // The row of actions shows on hover, and always under a turn's answer.
  const actions = (
    <MessageActions m={m} index={index} className={cn("mt-1 -ml-1.5 transition-opacity", !turnEnd && "md:opacity-0 md:group-hover:opacity-100 md:focus-within:opacity-100")} />
  );
  const [raw, setRaw] = React.useState(false);
  const [viewing, setViewing] = React.useState<number | null>(null);
  const images = (m.files ?? []).filter(isImage);
  const anchor = React.useContext(AnchorPrefix);
  if (m.role === "user") {
    // A /command it starts with is marked as one.
    const command = raw ? null : splitCommand(m.text ?? "");
    return (
      <div className="group flex scroll-mt-12 gap-3" id={`${anchor}${index}`}>
        <div className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground">
          <UserIcon className="size-3.5" />
        </div>
        <div className="min-w-0 flex-1">
          <ChapterMark index={index} />
          <div data-copy-skip className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
            <span className="font-medium text-foreground">you</span>
            <span title={stamp(m.at)}>{new Date(m.at).toLocaleTimeString()}</span>
            {m.steered && (
              <span className="rounded bg-sky-500/10 px-1.5 text-[10px] font-medium text-sky-700 dark:text-sky-300" title="Sent while the agent worked, and handed to it within that turn">
                sent while working
              </span>
            )}
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
              {command ? (
                <div className="flex items-start">
                  <span className="mt-0.5 shrink-0">
                    <CommandChip name={command.name} />
                  </span>
                  {command.rest && (
                    <div className="min-w-0 flex-1">
                      <Body text={command.rest} raw={false} />
                    </div>
                  )}
                </div>
              ) : (
                <Body text={m.text ?? ""} raw={raw} />
              )}
            </div>
          )}
          {m.files && m.files.length > 0 && (
            <div className="mt-1.5 flex flex-wrap gap-2">
              {m.files.map((f) =>
                isImage(f) ? (
                  <button
                    key={f.path}
                    type="button"
                    title={`${baseName(f.path)} · click to enlarge`}
                    className="cursor-zoom-in"
                    onClick={() => setViewing(images.findIndex((g) => g.path === f.path))}
                  >
                    {/* eslint-disable-next-line @next/next/no-img-element */}
                    <img src={attachmentURL(f.path)} alt="" className="max-h-40 max-w-60 rounded-lg border object-contain" />
                  </button>
                ) : (
                  <span key={f.path} className="text-xs text-muted-foreground">
                    attached: {f.path}
                  </span>
                ),
              )}
              <ImageViewer images={images.map((f) => ({ src: attachmentURL(f.path), name: baseName(f.path) }))} index={viewing} onIndex={setViewing} />
            </div>
          )}
          {!m.shell && actions}
        </div>
      </div>
    );
  }
  return (
    <div className="group flex scroll-mt-12 gap-3" id={`${anchor}${index}`}>
      <div className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border bg-background">
        <BotIcon className="size-3.5" />
      </div>
      <div className="min-w-0 flex-1 space-y-2">
        <ChapterMark index={index} />
        <div data-copy-skip className="flex items-center gap-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">agent</span>
          <span title={stamp(m.at)}>{new Date(m.at).toLocaleTimeString()}</span>
          <span className="opacity-0 group-hover:opacity-100">#{index}</span>
          {m.text && <RawToggle raw={raw} setRaw={setRaw} />}
        </div>
        {m.thinking && <Thinking text={m.thinking} />}
        {m.text && <Body text={m.text} raw={raw} />}
        {m.tools && m.tools.length > 0 && <ToolStack calls={m.tools} />}
        {m.err && <div className="rounded-lg border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">{m.err}</div>}
        {(m.text || turnEnd) && actions}
      </div>
    </div>
  );
}

export type Turn = { role: Message["role"]; items: { m: Message; index: number }[] };

/** groupTurns cuts a conversation where the speaker changes: each of your
 * messages stands alone, and the agent's messages between two of yours (one
 * per step it took) make one run. */
export function groupTurns(messages: Message[]): Turn[] {
  const out: Turn[] = [];
  messages.forEach((m, index) => {
    const last = out[out.length - 1];
    if (m.role === "assistant" && last?.role === "assistant") last.items.push({ m, index });
    else out.push({ role: m.role, items: [{ m, index }] });
  });
  return out;
}

/** duration is the time between two instants, the way a person says it. */
function duration(from: string, to: string): string {
  const s = Math.round((new Date(to).getTime() - new Date(from).getTime()) / 1000);
  if (!(s > 0)) return "";
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

type RunEntry =
  | { kind: "text"; m: Message; index: number }
  | { kind: "thinking"; text: string; index: number }
  | { kind: "tools"; calls: ToolCall[]; index: number }
  | { kind: "err"; text: string; index: number };

/** runEntries flattens a run of agent messages into what the reader sees, in
 * order: what it thought, what it said, and the calls it made — the calls of
 * messages that said nothing between them run together as one stack. */
function runEntries(items: { m: Message; index: number }[]): RunEntry[] {
  const out: RunEntry[] = [];
  for (const { m, index } of items) {
    if (m.thinking) out.push({ kind: "thinking", text: m.thinking, index });
    if (m.text?.trim()) out.push({ kind: "text", m, index });
    if (m.tools?.length) {
      const last = out[out.length - 1];
      if (last?.kind === "tools") last.calls = [...last.calls, ...m.tools];
      else out.push({ kind: "tools", calls: m.tools, index });
    }
    if (m.err) out.push({ kind: "err", text: m.err, index });
  }
  return out;
}

/** RunText is one thing the agent said within a run, with its actions. */
function RunText({ m, index, final }: { m: Message; index: number; final: boolean }) {
  const [raw, setRaw] = React.useState(false);
  // The answer keeps its actions under it; narration between calls gets them
  // as a toolbar over its corner on hover, so it takes no room of its own.
  return (
    <div className="group/text relative">
      <Body text={m.text ?? ""} raw={raw} />
      <div
        className={cn(
          "flex items-center gap-2 text-xs text-muted-foreground transition-opacity",
          "mt-1 -ml-1.5",
          // Phones have no hover: there the row stays in place.
          !final &&
            "md:absolute md:-top-4 md:right-0 md:z-10 md:m-0 md:rounded-md md:border md:bg-background md:px-1 md:opacity-0 md:shadow-sm md:group-hover/text:opacity-100 md:focus-within:opacity-100",
        )}
      >
        <MessageActions m={m} index={index} />
        <button className="hover:text-foreground hover:underline" onClick={() => setRaw(!raw)}>
          {raw ? "rendered" : "raw"}
        </button>
        <span title={stamp(m.at)}>#{index}</span>
      </div>
    </div>
  );
}

/**
 * AssistantRun draws the agent's messages between two of yours as one block,
 * as Claude Code does: one header, then what it said and the calls it made, in
 * order. Each call is a line (state, tool, command); calls with nothing said
 * between them stack, and a run of looking-around calls folds into one line.
 * Messages keep their anchors (#m<index>) for chapters and links.
 */
export function AssistantRun({
  items,
  since,
  turnEnd,
  tail,
}: {
  items: { m: Message; index: number }[];
  /** since: when the message this run answers was sent, so the turn's time counts from it. */
  since?: string;
  turnEnd?: boolean;
  tail?: React.ReactNode;
}) {
  const first = items[0];
  const last = items[items.length - 1];
  const entries = runEntries(items);
  const lastText = entries.findLastIndex((e) => e.kind === "text");
  const calls = entries.reduce((n, e) => n + (e.kind === "tools" ? e.calls.length : 0), 0);
  const took = turnEnd ? duration(since ?? first.m.at, last.m.at) : "";
  const anchor = React.useContext(AnchorPrefix);
  return (
    <div className="group flex gap-3">
      <div className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border bg-background">
        <BotIcon className={cn("size-3.5", tail && "animate-pulse")} />
      </div>
      <div className="min-w-0 flex-1 space-y-2">
        <div>
          {items.map(({ index }) => (
            // Anchors first, so a jump to any message of the run lands on the
            // run; empty ones take no room.
            <div key={index} id={`${anchor}${index}`} className="scroll-mt-12">
              <ChapterMark index={index} />
            </div>
          ))}
          <div data-copy-skip className="flex items-center gap-2 text-xs text-muted-foreground">
            <span className="font-medium text-foreground">agent</span>
            <span title={stamp(first.m.at)}>{new Date(first.m.at).toLocaleTimeString()}</span>
            {took && <span title="From your message to the agent's last step">· {took}</span>}
            {calls > 1 && <span>· {calls} calls</span>}
            <span className="opacity-0 group-hover:opacity-100">#{items.length > 1 ? `${first.index}–${last.index}` : first.index}</span>
          </div>
        </div>
        {entries.map((e, i) =>
          e.kind === "text" ? (
            <RunText key={`t${e.index}`} m={e.m} index={e.index} final={!!turnEnd && i === lastText} />
          ) : e.kind === "thinking" ? (
            <Thinking key={`k${e.index}`} text={e.text} />
          ) : e.kind === "tools" ? (
            <ToolStack key={`c${e.index}`} calls={e.calls} />
          ) : (
            <div key={`e${e.index}`} className="rounded-lg border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">
              {e.text}
            </div>
          ),
        )}
        {turnEnd && lastText < 0 && <MessageActions m={last.m} index={last.index} className="-ml-1.5" />}
        {tail}
      </div>
    </div>
  );
}

/** LiveTail is the turn in flight: streamed text, running tools, and the
 * questions waiting on someone — answerable from here as from the terminal.
 * Inside an AssistantRun (attached) it continues that block instead of
 * opening one of its own. */
export function LiveTail({
  live,
  onApprove,
  onChoose,
  attached,
}: {
  live?: Live;
  onApprove: (id: string, verdict: "allow" | "allow_all" | "deny") => void;
  onChoose: (id: string, index: number) => void;
  attached?: boolean;
}) {
  if (!live || (!live.busy && !live.error)) return null;
  if (!live.busy && live.error) {
    return <div className="rounded-lg border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">turn ended: {live.error}</div>;
  }
  // What the agent is doing now: under its last step when attached, as
  // Claude Code keeps "Working…" at the bottom; at the top of its own block
  // otherwise.
  const status = (
    <div className="flex items-center gap-2 text-xs text-sky-600 dark:text-sky-400">
      <span className="relative flex size-2">
        <span className="absolute inline-flex size-full animate-ping rounded-full bg-sky-400 opacity-60" />
        <span className="relative inline-flex size-2 rounded-full bg-sky-500" />
      </span>
      {live.status || "working"}
    </div>
  );
  const body = (
    <div className="min-w-0 flex-1 space-y-2">
      {live.thinking && <Thinking text={live.thinking} live />}
      {live.partial && <Markdown text={live.partial} streaming />}
      {Object.keys(live.running).length > 0 && (
        <div className="-ml-1.5">
          {Object.values(live.running).map((c) => (
            <ToolRow key={c.id} call={c} output={live.output[c.id]} running />
          ))}
        </div>
      )}
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
      {attached && status}
    </div>
  );
  if (attached) return body;
  return (
    <div className="flex gap-3">
      <div className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border bg-background">
        <BotIcon className="size-3.5 animate-pulse" />
      </div>
      <div className="min-w-0 flex-1 space-y-2">
        {status}
        {body}
      </div>
    </div>
  );
}
