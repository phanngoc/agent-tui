"use client";

import * as React from "react";
import { CornerDownLeftIcon, LightbulbIcon, SparklesIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { SYSTEM_COMMANDS, agentCommand, parseCommand, typingCommand, type SystemCommand } from "@/lib/commands";

// The composer's side of system commands: the command marked in the text as
// it is typed, a menu of them after "/", a line saying what the one typed
// will do, and the tip that says they exist at all.

/** The look of a command the admin carries out itself, wherever it shows. */
export const systemTone = "bg-violet-500/15 text-violet-700 ring-1 ring-violet-500/40 dark:text-violet-300";

/** CommandChip is a command a sent message starts with. A system command
 * is carried out and never sent, so one in a transcript went to the agent. */
export function CommandChip({ name }: { name: string }) {
  return (
    <span title="Sent to the agent as a command" className="mr-1.5 inline-block rounded bg-background px-1 font-mono text-[0.92em] font-medium ring-1 ring-border">
      /{name}
    </span>
  );
}

/** splitCommand finds the /command a sent message starts with, for the
 * transcript to mark it; the rest is the message. */
export function splitCommand(text: string): { name: string; rest: string } | null {
  const m = /^\/([a-z][\w:.-]*)(?=\s|$)/i.exec(text);
  return m ? { name: m[1], rest: text.slice(m[0].length).trim() } : null;
}

/**
 * CommandBackdrop sits under a transparent textarea and paints a box behind
 * a system command at the start of its text. The text itself is the
 * textarea's, so the caret, selection and IME behave as ever; only this
 * layer knows the command is there. It must share the textarea's padding,
 * font and wrapping, and follows its scroll.
 */
export function CommandBackdrop({ text, scrollTop, className }: { text: string; scrollTop: number; className?: string }) {
  const p = parseCommand(text);
  const a = p ? null : agentCommand(text);
  const ref = React.useRef<HTMLDivElement>(null);
  React.useLayoutEffect(() => {
    if (ref.current) ref.current.scrollTop = scrollTop;
  }, [scrollTop]);
  if (!p && !a) return null;
  const end = p ? p.end : a!.length + 1;
  return (
    <div ref={ref} aria-hidden className={cn("pointer-events-none absolute inset-0 overflow-hidden break-words whitespace-pre-wrap text-transparent", className)}>
      <mark className={cn("rounded-sm text-transparent", p ? "bg-violet-500/20 ring-1 ring-violet-500/50" : "bg-muted ring-1 ring-border")}>{text.slice(0, end)}</mark>
      {text.slice(end)}
    </div>
  );
}

/** useCommandMenu drives the menu shown while a command's name is typed:
 * ↑/↓ to move, Tab or Enter to take one, Esc to put it away. */
export function useCommandMenu({ text, busy, onPick }: { text: string; busy: boolean; onPick: (c: SystemCommand) => void }) {
  const typed = typingCommand(text);
  const [dismissed, setDismissed] = React.useState<string | null>(null);
  const [sel, setSel] = React.useState(0);
  const items = React.useMemo(() => (typed === null ? [] : SYSTEM_COMMANDS.filter((c) => c.name.startsWith(typed) && (busy || !c.busyOnly))), [typed, busy]);
  const open = items.length > 0 && dismissed !== text;
  const at = Math.min(sel, Math.max(0, items.length - 1));
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (!open) return false;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      setSel((at + (e.key === "ArrowDown" ? 1 : items.length - 1)) % items.length);
      return true;
    }
    if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing)) {
      e.preventDefault();
      setSel(0);
      onPick(items[at]);
      return true;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      setDismissed(text);
      return true;
    }
    return false;
  };
  return { open, items, at, onKeyDown, pick: onPick, hover: setSel };
}

/** CommandMenu lists the system commands matching what is typed, above the
 * composer. */
export function CommandMenu({ menu }: { menu: ReturnType<typeof useCommandMenu> }) {
  if (!menu.open) return null;
  return (
    <div className="absolute right-0 bottom-full left-0 z-20 mb-2 overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-lg" role="listbox">
      <div className="flex items-center gap-1.5 border-b px-3 py-1.5 text-[11px] font-medium text-muted-foreground">
        <SparklesIcon className="size-3 text-violet-500" /> Handled by agent-tui — not sent to the agent
      </div>
      {menu.items.map((c, i) => (
        <button
          key={c.name}
          type="button"
          role="option"
          aria-selected={i === menu.at}
          onMouseEnter={() => menu.hover(i)}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => menu.pick(c)}
          className={cn("flex w-full items-baseline gap-2 px-3 py-1.5 text-left text-sm", i === menu.at && "bg-muted")}
        >
          <span className={cn("shrink-0 rounded px-1 font-mono text-xs font-medium", systemTone)}>/{c.name}</span>
          {c.args && <span className="shrink-0 font-mono text-xs text-muted-foreground">{c.args}</span>}
          <span className="min-w-0 truncate text-xs text-muted-foreground">{c.summary}</span>
          {i === menu.at && <CornerDownLeftIcon className="ml-auto size-3 shrink-0 self-center text-muted-foreground" />}
        </button>
      ))}
      <div className="border-t px-3 py-1.5 text-[11px] text-muted-foreground">
        Any other <span className="font-mono">/command</span> goes to the agent as typed (skills, its own commands) · start with <span className="font-mono">{"//"}</span> to send one of these names to
        the agent instead
      </div>
    </div>
  );
}

/** CommandHint says, under the text, what the command typed will do. */
export function CommandHint({ text, className }: { text: string; className?: string }) {
  const p = parseCommand(text);
  const a = p ? null : agentCommand(text);
  if (p)
    return (
      <div className={cn("flex min-w-0 items-center gap-1.5 text-[11px] text-violet-700 dark:text-violet-300", className)}>
        <SparklesIcon className="size-3 shrink-0" />
        <span className="truncate">
          <span className="font-mono font-medium">/{p.cmd.name}</span> · {p.cmd.summary}
          {p.cmd.args && !p.arg && <span className="text-muted-foreground"> — type {p.cmd.args} after it</span>}
        </span>
      </div>
    );
  if (a)
    return (
      <div className={cn("truncate text-[11px] text-muted-foreground", className)}>
        <span className="font-mono">/{a}</span> goes to the agent as a command
      </div>
    );
  const escaped = /^\/\/([a-z][\w-]*)/i.exec(text);
  if (escaped)
    return (
      <div className={cn("truncate text-[11px] text-muted-foreground", className)}>
        goes to the agent as <span className="font-mono">/{escaped[1]}</span>, not run here
      </div>
    );
  return null;
}

/** CommandTip is the faint line that says commands exist, while the box is
 * empty: one tip, the one that fits the moment. */
export function CommandTip({ busy, className }: { busy: boolean; className?: string }) {
  return (
    <div className={cn("flex min-w-0 items-center gap-1 text-[11px] text-muted-foreground", className)}>
      <LightbulbIcon className="size-3 shrink-0" />
      <span className="truncate">
        {busy ? (
          <>
            Question while it works? <span className="font-mono text-violet-700 dark:text-violet-300">/btw</span> asks on the side without interrupting
          </>
        ) : (
          <>
            Type <span className="font-mono">/</span> for commands · <span className="font-mono text-violet-700 dark:text-violet-300">/btw</span> asks a side question
          </>
        )}
      </span>
    </div>
  );
}
