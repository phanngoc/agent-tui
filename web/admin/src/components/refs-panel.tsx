"use client";

import * as React from "react";
import {
  BookOpenIcon,
  ExternalLinkIcon,
  FileSpreadsheetIcon,
  FileTextIcon,
  GitPullRequestIcon,
  GlobeIcon,
  HashIcon,
  LocateFixedIcon,
  PinIcon,
  PinOffIcon,
  PlusIcon,
  PresentationIcon,
  TicketIcon,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useVersion } from "@/lib/store";
import type { BoardColumn, Link, PinnedRef } from "@/lib/types";
import { cn } from "@/lib/utils";
import { Ago, Empty, ErrorNote } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

type Refs = { pinned: PinnedRef[]; found: Link[]; origin?: Link | null };

/** KIND says how each kind of link is drawn and named. */
const KIND: Record<string, { icon: React.ElementType; name: string; tone: string }> = {
  slack: { icon: HashIcon, name: "Slack", tone: "text-fuchsia-600 dark:text-fuchsia-400" },
  github: { icon: GitPullRequestIcon, name: "GitHub", tone: "text-foreground" },
  google: { icon: FileSpreadsheetIcon, name: "Google", tone: "text-emerald-600 dark:text-emerald-400" },
  backlog: { icon: TicketIcon, name: "Backlog", tone: "text-green-600 dark:text-green-400" },
  jira: { icon: TicketIcon, name: "Jira", tone: "text-blue-600 dark:text-blue-400" },
  confluence: { icon: BookOpenIcon, name: "Confluence", tone: "text-blue-600 dark:text-blue-400" },
  notion: { icon: FileTextIcon, name: "Notion", tone: "text-foreground" },
  miro: { icon: PresentationIcon, name: "Miro", tone: "text-amber-600 dark:text-amber-400" },
  web: { icon: GlobeIcon, name: "Web", tone: "text-muted-foreground" },
};

export function LinkKindIcon({ kind, className }: { kind: string; className?: string }) {
  const k = KIND[kind] ?? KIND.web;
  const Icon = k.icon;
  return <Icon className={cn("size-3.5 shrink-0", k.tone, className)} aria-label={k.name} />;
}

/** LinkChip is a link, small: its kind's icon and its label, opening in a new tab. */
export function LinkChip({ link, className }: { link: Pick<Link, "url" | "kind" | "label">; className?: string }) {
  return (
    <a
      href={link.url}
      target="_blank"
      rel="noreferrer"
      title={link.url}
      onClick={(e) => e.stopPropagation()}
      onPointerDown={(e) => e.stopPropagation()}
      className={cn("inline-flex max-w-full min-w-0 items-center gap-1 rounded-md border bg-background px-1.5 py-0.5 text-[11px] hover:bg-muted", className)}
    >
      <LinkKindIcon kind={link.kind} className="size-3" />
      <span className="truncate">{link.label}</span>
    </a>
  );
}

export const BOARD_COLUMNS: { id: BoardColumn; label: string; dot: string }[] = [
  { id: "backlog", label: "Backlog", dot: "bg-muted-foreground/50" },
  { id: "todo", label: "Todo", dot: "bg-sky-500" },
  { id: "doing", label: "In progress", dot: "bg-amber-500" },
  { id: "done", label: "Done", dot: "bg-emerald-500" },
];

/** BoardPicker moves a conversation between the board's columns. */
export function BoardPicker({ id, value, onMoved, className }: { id: string; value?: string; onMoved?: (c: BoardColumn) => void; className?: string }) {
  const cur = (value || "backlog") as BoardColumn;
  return (
    <div className={cn("flex flex-wrap rounded-md border p-0.5 text-xs", className)} role="radiogroup" aria-label="Board column">
      {BOARD_COLUMNS.map((c) => (
        <button
          key={c.id}
          role="radio"
          aria-checked={cur === c.id}
          className={cn("flex items-center gap-1.5 rounded px-2 py-1", cur === c.id ? "bg-muted font-medium" : "text-muted-foreground hover:text-foreground")}
          onClick={async () => {
            if (cur === c.id) return;
            onMoved?.(c.id);
            try {
              // Unranked in the new column: it goes among the newest.
              await api.put(`/api/sessions/${id}/settings`, { board: c.id, board_rank: 0 });
            } catch (e) {
              toast.error((e as Error).message);
            }
          }}
        >
          <span className={cn("size-2 rounded-full", c.dot)} />
          {c.label}
        </button>
      ))}
    </div>
  );
}

/**
 * RefsPanel is where a conversation came from and what it pointed at: the
 * links pinned to it, and every link said in it, with who said it and the
 * words around it — so an analysis can be traced back to the Slack thread
 * or the issue it started from.
 */
export function RefsPanel({ id, onJump }: { id: string; onJump?: (at: number) => void }) {
  const v = useVersion("sessions");
  const { data, error, reload, setData } = useFetch<Refs>(`/api/sessions/${id}/refs`, [v]);
  const [url, setUrl] = React.useState("");
  const [title, setTitle] = React.useState("");
  const [kind, setKind] = React.useState("");

  const save = async (pinned: { url: string; title?: string; added?: string }[]) => {
    try {
      await api.put(`/api/sessions/${id}/settings`, { refs: pinned });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setTimeout(reload, 300);
    }
  };
  const pins = data?.pinned ?? [];
  const pinnedURLs = new Set(pins.map((p) => p.url));
  const pin = (l: { url: string; title?: string }) => {
    if (pinnedURLs.has(l.url)) return;
    setData((d) => d && { ...d, pinned: [...d.pinned, { url: l.url, title: l.title, added: new Date().toISOString(), kind: "web", label: l.title || l.url }] });
    void save([...pins, { url: l.url, title: l.title }]);
  };
  const unpin = (u: string) => {
    setData((d) => d && { ...d, pinned: d.pinned.filter((p) => p.url !== u) });
    void save(pins.filter((p) => p.url !== u));
  };

  const found = data?.found ?? [];
  const kinds = Array.from(new Set(found.map((l) => l.kind)));
  const shown = kind ? found.filter((l) => l.kind === kind) : found;
  const origin = data?.origin;

  return (
    <div className="space-y-5 text-sm">
      <ErrorNote error={error} />
      {origin && (
        <div className="rounded-lg border bg-muted/40 p-3">
          <div className="mb-1 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">Source</div>
          <a href={origin.url} target="_blank" rel="noreferrer" className="flex items-center gap-2 font-medium hover:underline">
            <LinkKindIcon kind={origin.kind} className="size-4" />
            <span className="min-w-0 truncate">{origin.label}</span>
            <ExternalLinkIcon className="size-3.5 shrink-0 text-muted-foreground" />
          </a>
          <div className="mt-1 truncate font-mono text-[11px] text-muted-foreground" title={origin.url}>
            {origin.url}
          </div>
          {origin.at >= 0 && onJump && (
            <button className="mt-2 flex items-center gap-1 text-xs text-primary hover:underline" onClick={() => onJump(origin.at)}>
              <LocateFixedIcon className="size-3" /> where it was given (message {origin.at + 1})
            </button>
          )}
        </div>
      )}

      <section className="space-y-2">
        <div className="flex items-center gap-2">
          <PinIcon className="size-3.5 text-muted-foreground" />
          <h3 className="font-medium">Pinned</h3>
          <span className="text-xs text-muted-foreground">{pins.length}</span>
        </div>
        {pins.length === 0 && <p className="text-xs text-muted-foreground">Pin the thread or issue this came from, so the board and the list show it as the source.</p>}
        {pins.map((p) => (
          <div key={p.url} className="group flex items-start gap-2 rounded-md border px-2 py-1.5">
            <LinkKindIcon kind={p.kind} className="mt-0.5" />
            <a href={p.url} target="_blank" rel="noreferrer" className="min-w-0 flex-1 hover:underline">
              <div className="truncate font-medium">{p.title || p.label}</div>
              <div className="truncate font-mono text-[11px] text-muted-foreground">{p.url}</div>
            </a>
            <button className="text-muted-foreground hover:text-foreground" title="Unpin" aria-label="Unpin" onClick={() => unpin(p.url)}>
              <PinOffIcon className="size-3.5" />
            </button>
          </div>
        ))}
        <form
          className="flex flex-wrap gap-1.5"
          onSubmit={(e) => {
            e.preventDefault();
            const u = url.trim();
            if (!/^https?:\/\/\S+$/.test(u)) {
              toast.error("Paste a web link, starting with https://");
              return;
            }
            pin({ url: u, title: title.trim() || undefined });
            setUrl("");
            setTitle("");
          }}
        >
          <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://… a Slack thread, an issue, a doc" className="h-8 min-w-0 flex-[2] text-xs" />
          <Input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="name (optional)" className="h-8 min-w-0 flex-1 text-xs" />
          <Button type="submit" size="sm" variant="outline" className="h-8" disabled={!url.trim()}>
            <PlusIcon /> Pin
          </Button>
        </form>
      </section>

      <section className="space-y-2">
        <div className="flex items-center gap-2">
          <h3 className="font-medium">In the conversation</h3>
          <span className="text-xs text-muted-foreground">{found.length}</span>
        </div>
        {kinds.length > 1 && (
          <div className="flex flex-wrap gap-1">
            {["", ...kinds].map((k) => (
              <button
                key={k || "all"}
                onClick={() => setKind(k)}
                className={cn("flex items-center gap-1 rounded-md border px-2 py-0.5 text-xs", kind === k ? "bg-muted font-medium" : "text-muted-foreground hover:text-foreground")}
              >
                {k && <LinkKindIcon kind={k} className="size-3" />}
                {k ? (KIND[k]?.name ?? k) : "All"} <span className="text-muted-foreground">{k ? found.filter((l) => l.kind === k).length : found.length}</span>
              </button>
            ))}
          </div>
        )}
        {data && found.length === 0 && <Empty title="No links said yet" />}
        {shown.map((l) => (
          <div key={l.url} className="rounded-md border px-2 py-1.5">
            <div className="flex items-center gap-2">
              <LinkKindIcon kind={l.kind} />
              <a href={l.url} target="_blank" rel="noreferrer" className="min-w-0 flex-1 truncate font-medium hover:underline" title={l.url}>
                {l.label}
              </a>
              {!pinnedURLs.has(l.url) && (
                <button className="text-muted-foreground hover:text-foreground" title="Pin to the conversation" aria-label="Pin" onClick={() => pin({ url: l.url })}>
                  <PinIcon className="size-3.5" />
                </button>
              )}
            </div>
            {l.snippet && <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{l.snippet}</p>}
            <div className="mt-1 flex items-center gap-1.5 text-[11px] text-muted-foreground">
              <span>{l.role === "user" ? "you" : "agent"}</span>
              {l.when && (
                <>
                  <span>·</span>
                  <Ago at={l.when} />
                </>
              )}
              {(l.times ?? 1) > 1 && (
                <>
                  <span>·</span>
                  <span>in {l.times} messages</span>
                </>
              )}
              {onJump && (
                <button className="ml-auto flex items-center gap-1 text-primary hover:underline" onClick={() => onJump(l.at)}>
                  <LocateFixedIcon className="size-3" /> message {l.at + 1}
                </button>
              )}
            </div>
          </div>
        ))}
      </section>
    </div>
  );
}
