"use client";

import * as React from "react";
import { BookmarkIcon, CheckIcon, CopyIcon, GitForkIcon, HashIcon, LibraryIcon, ListIcon, PinIcon, SquareIcon, Volume2Icon } from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import type { Message } from "@/lib/types";
import { Ago } from "@/components/common";
import { canSpeak, useSpeech } from "@/lib/speech";
import { copyMarkdownForSlack } from "@/components/slack-copy";

/** What a conversation lets its messages do; the transcript is read-only without it. */
export interface MessageHooks {
  session: string;
  messages: Message[];
  chapters: number[];
  /** fork starts a conversation from this message. */
  fork: (index: number) => void;
  chapter: (index: number, on: boolean) => void;
  /** toWiki puts a message into the project's wiki, read into its pages. */
  toWiki?: (index: number) => void;
}

export const MessageHooksContext = React.createContext<MessageHooks | null>(null);

function Act({ title, onClick, active, children }: { title: string; onClick: () => void; active?: boolean; children: React.ReactNode }) {
  return (
    <button
      title={title}
      aria-label={title}
      onClick={onClick}
      className={cn("grid size-7 place-items-center rounded-md hover:bg-muted hover:text-foreground", active && "bg-primary/10 text-primary hover:bg-primary/15 hover:text-primary")}
    >
      {children}
    </button>
  );
}

/**
 * MessageActions is the row under a message, as in the Claude app: copy, fork
 * from here, pin as chapter, read aloud, and when it was said.
 */
export function MessageActions({ m, index, className }: { m: Message; index: number; className?: string }) {
  const hooks = React.useContext(MessageHooksContext);
  const [copied, setCopied] = React.useState(false);
  const key = `${hooks?.session ?? ""}#${index}`;
  const reading = useSpeech((s) => s.reading === key);
  const text = m.text ?? "";
  if (!hooks) return null;
  const chapter = hooks.chapters.includes(index);
  const user = m.role === "user";
  return (
    <div data-copy-skip className={cn("flex items-center gap-0.5 text-muted-foreground", className)}>
      {text && (
        <Act
          title={copied ? "Copied" : "Copy"}
          onClick={() =>
            void navigator.clipboard.writeText(text).then(
              () => {
                setCopied(true);
                setTimeout(() => setCopied(false), 1500);
              },
              () => toast.error("Could not copy"),
            )
          }
        >
          {copied ? <CheckIcon className="size-3.5" /> : <CopyIcon className="size-3.5" />}
        </Act>
      )}
      {text && !user && (
        <Act
          title="Copy for Slack: formatted to paste into a Slack message"
          onClick={() =>
            void copyMarkdownForSlack(text).then(
              () => toast.success("Copied for Slack", { description: "Paste it into a Slack message." }),
              (e: Error) => toast.error(`Could not copy: ${e.message}`),
            )
          }
        >
          <HashIcon className="size-3.5" />
        </Act>
      )}
      <Act title={user ? "Fork from here: a new conversation up to this prompt, with it ready to edit" : "Fork from here: a new conversation up to this message"} onClick={() => hooks.fork(index)}>
        <GitForkIcon className="size-3.5" />
      </Act>
      {text && hooks.toWiki && (
        <Act title="Add to wiki: kept as a document and read into the wiki's pages" onClick={() => hooks.toWiki?.(index)}>
          <LibraryIcon className="size-3.5" />
        </Act>
      )}
      <Act title={chapter ? "Unpin chapter" : "Pin as chapter"} active={chapter} onClick={() => hooks.chapter(index, !chapter)}>
        <PinIcon className={cn("size-3.5", chapter && "fill-current")} />
      </Act>
      {text && canSpeak() && (
        <Act title={reading ? "Stop reading" : "Read aloud"} active={reading} onClick={() => (reading ? useSpeech.getState().stop() : useSpeech.getState().speak(key, text))}>
          {reading ? <SquareIcon className="size-3 fill-current" /> : <Volume2Icon className="size-3.5" />}
        </Act>
      )}
      <span className="ml-1.5 text-xs">
        <Ago at={m.at} />
      </span>
    </div>
  );
}

/** chapterLabel names a chapter: the message's first heading, else its first line, else the prompt before it. */
export function chapterLabel(messages: Message[], index: number): string {
  const first = (t?: string) =>
    (t ?? "")
      .split("\n")
      .map((l) => l.trim())
      .find((l) => l && !l.startsWith("```"))
      ?.replace(/^#{1,6}\s+|[*_`]/g, "") ?? "";
  const m = messages[index];
  const heading = (m?.text ?? "").match(/^\s{0,3}#{1,6}\s+(.+)$/m)?.[1]?.replace(/[*_`]/g, "");
  let label = heading || first(m?.text);
  for (let i = index; !label && i >= 0; i--) if (messages[i].role === "user") label = first(messages[i].text);
  label = label || `Message ${index}`;
  return label.length > 70 ? label.slice(0, 69) + "…" : label;
}

/** ChapterMark heads a message pinned as a chapter. */
export function ChapterMark({ index }: { index: number }) {
  const hooks = React.useContext(MessageHooksContext);
  if (!hooks?.chapters.includes(index)) return null;
  const label = chapterLabel(hooks.messages, index);
  return (
    <div data-copy-skip className="mb-1 flex items-center gap-2 text-[11px] font-medium tracking-wide text-primary uppercase">
      <BookmarkIcon className="size-3 fill-current" />
      <span className="truncate normal-case">{label}</span>
      <span className="h-px flex-1 bg-primary/20" />
    </div>
  );
}

/** Chapters lists a conversation's chapters, to jump to one. */
export function Chapters({ messages, chapters, onJump }: { messages: Message[]; chapters: number[]; onJump: (index: number) => void }) {
  const [open, setOpen] = React.useState(false);
  const ref = React.useRef<HTMLDivElement>(null);
  React.useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false);
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", esc);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", esc);
    };
  }, [open]);
  const list = chapters.filter((i) => i < messages.length);
  if (!list.length) return null;
  return (
    <div ref={ref} className="pointer-events-auto relative">
      <button
        onClick={() => setOpen(!open)}
        title="Chapters"
        className={cn("flex items-center gap-1.5 rounded-full border bg-background/95 px-2.5 py-1 text-xs shadow-sm backdrop-blur hover:bg-muted", open && "bg-muted")}
      >
        <ListIcon className="size-3.5" />
        Chapters <span className="text-muted-foreground">{list.length}</span>
      </button>
      {open && (
        <div role="menu" className="absolute top-full right-0 z-30 mt-1 max-h-80 w-72 overflow-auto rounded-md border bg-popover py-1 text-[13px] text-popover-foreground shadow-lg">
          {list.map((i, n) => (
            <button
              key={i}
              role="menuitem"
              onClick={() => {
                setOpen(false);
                onJump(i);
              }}
              className="flex w-full items-baseline gap-2 px-3 py-1.5 text-left hover:bg-muted"
            >
              <span className="w-4 shrink-0 text-right text-[11px] text-muted-foreground">{n + 1}</span>
              <span className="min-w-0 flex-1 truncate">{chapterLabel(messages, i)}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
