"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { AlarmClockIcon, LinkIcon, ListIcon, MessageSquareTextIcon, PinIcon, SearchIcon } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useGateway, useVersion } from "@/lib/store";
import type { BoardColumn, Summary } from "@/lib/types";
import { baseName } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Ago, Dot, ErrorNote, PageHeader } from "@/components/common";
import { ProjectSwitcher } from "@/components/project-switcher";
import { BOARD_COLUMNS, BoardPicker, LinkChip, RefsPanel } from "@/components/refs-panel";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";

// The board reads conversations as tasks. Where a card sits is the user's
// call, saved on the conversation, and has nothing to do with whether its
// agent is running: a turn starting or ending never moves a card.

const colOf = (s: Summary): BoardColumn => (s.board || "backlog") as BoardColumn;

// Ranked cards first, lowest rank on top; the rest newest first.
function order(a: Summary, b: Summary) {
  const ra = a.board_rank || Infinity;
  const rb = b.board_rank || Infinity;
  if (ra !== rb) return ra - rb;
  return b.updated.localeCompare(a.updated);
}

const STEP = 1000;
// How many unranked cards one drop may number; past that a card dropped among
// them goes to the end of the ranked ones instead.
const MAX_RENUMBER = 30;

/**
 * placeAt works out the rank a card gets when dropped at index i of a column
 * (the column without it), and which unranked cards above that spot must be
 * numbered first so the order on screen is the order kept.
 */
function placeAt(list: Summary[], i: number): { rank: number; renumber: { id: string; rank: number }[] } {
  const prev = list[i - 1];
  const next = list[i];
  const pr = prev?.board_rank || 0;
  const nr = next?.board_rank || 0;
  if (!prev) return { rank: nr ? nr / 2 : STEP, renumber: [] };
  if (pr && nr) return { rank: (pr + nr) / 2, renumber: [] };
  if (pr) return { rank: pr + STEP, renumber: [] };
  // Dropped among unranked cards: number those above the spot, in the order shown.
  const firstUnranked = list.findIndex((s) => !s.board_rank);
  const lastRanked = firstUnranked > 0 ? list[firstUnranked - 1].board_rank || 0 : 0;
  const count = i - firstUnranked;
  if (count > MAX_RENUMBER) return { rank: lastRanked + STEP, renumber: [] };
  const renumber = list.slice(firstUnranked, i).map((s, k) => ({ id: s.id, rank: lastRanked + STEP * (k + 1) }));
  return { rank: lastRanked + STEP * (count + 1), renumber };
}

export default function BoardPage() {
  const router = useRouter();
  const root = useGateway((s) => s.root);
  const live = useGateway((s) => s.live);
  const [all, setAll] = React.useState(false);
  const [q, setQ] = React.useState("");
  const v = useVersion("sessions");
  const { data, error, reload, setData } = useFetch<Summary[]>("/api/sessions" + qs({ root: all ? "" : root, q, archived: "0" }), [v]);
  const [open, setOpen] = React.useState<string | null>(null);
  const [drag, setDrag] = React.useState<{ id: string; col?: BoardColumn; at?: number } | null>(null);

  const cards = React.useMemo(() => (data ?? []).filter((s) => !s.side_of), [data]);
  const columns = React.useMemo(() => {
    const by: Record<BoardColumn, Summary[]> = { backlog: [], todo: [], doing: [], done: [] };
    for (const s of cards) by[colOf(s)].push(s);
    for (const c of Object.values(by)) c.sort(order);
    return by;
  }, [cards]);

  const patch = (id: string, p: Partial<Summary>) => setData((d) => d?.map((s) => (s.id === id ? { ...s, ...p } : s)));

  const drop = async (id: string, col: BoardColumn, at: number) => {
    const card = cards.find((s) => s.id === id);
    if (!card) return;
    const list = columns[col].filter((s) => s.id !== id);
    const { rank, renumber } = placeAt(list, Math.min(at, list.length));
    patch(id, { board: col, board_rank: rank });
    for (const r of renumber) patch(r.id, { board_rank: r.rank });
    try {
      await Promise.all([
        ...renumber.map((r) => api.put(`/api/sessions/${r.id}/settings`, { board_rank: r.rank })),
        api.put(`/api/sessions/${id}/settings`, { board: col, board_rank: rank }),
      ]);
      if (colOf(card) !== col) toast.success(`«${card.title || "untitled"}» → ${BOARD_COLUMNS.find((c) => c.id === col)?.label}`);
    } catch (e) {
      toast.error((e as Error).message);
      reload();
    }
  };

  const multi = all || !root;
  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Board"
        description="Your conversations as tasks. Drag a card between columns, or open it to move it, read where it came from and jump to the conversation. A card stays where you put it, whatever its agent is doing."
        actions={
          <Link href="/sessions" className={buttonVariants({ variant: "outline", size: "sm" })}>
            <ListIcon /> List view
          </Link>
        }
      />
      <div className="flex flex-wrap items-center gap-2 border-b px-6 py-3">
        <div className="relative w-64 max-w-full">
          <SearchIcon className="absolute top-2 left-2 size-4 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search the cards" className="h-8 pl-8" />
        </div>
        <ProjectSwitcher variant="bar" className="w-64 max-w-full" onPicked={() => setAll(false)} />
        {root && (
          <div className="flex rounded-md border p-0.5 text-xs" role="group" aria-label="Which conversations">
            {[
              { on: !all, label: "This project" },
              { on: all, label: "All" },
            ].map((o) => (
              <button
                key={o.label}
                aria-pressed={o.on}
                onClick={() => setAll(o.label === "All")}
                className={cn("rounded px-2 py-0.5", o.on ? "bg-muted font-medium" : "text-muted-foreground hover:text-foreground")}
              >
                {o.label}
              </button>
            ))}
          </div>
        )}
        <span className="ml-auto text-xs text-muted-foreground">{cards.length} conversations · archived ones are left out</span>
      </div>
      <ErrorNote error={error} className="m-3" />
      <div className="grid min-h-0 flex-1 grid-cols-[repeat(4,minmax(260px,1fr))] gap-3 overflow-auto p-4">
        {BOARD_COLUMNS.map((c) => {
          const list = columns[c.id];
          const over = drag?.col === c.id;
          return (
            <section
              key={c.id}
              aria-label={c.label}
              className={cn("flex min-h-0 flex-col rounded-xl border bg-muted/30", over && "border-primary/60 bg-primary/5")}
              onDragOver={(e) => {
                if (!drag) return;
                e.preventDefault();
                e.dataTransfer.dropEffect = "move";
                // Where in the column: before the first card whose middle is below the pointer.
                const els = Array.from(e.currentTarget.querySelectorAll<HTMLElement>("[data-card]")).filter((el) => el.dataset.card !== drag.id);
                let at = els.length;
                for (let k = 0; k < els.length; k++) {
                  const r = els[k].getBoundingClientRect();
                  if (e.clientY < r.top + r.height / 2) {
                    at = k;
                    break;
                  }
                }
                if (drag.col !== c.id || drag.at !== at) setDrag({ ...drag, col: c.id, at });
              }}
              onDragLeave={(e) => {
                if (!e.currentTarget.contains(e.relatedTarget as Node) && drag?.col === c.id) setDrag({ id: drag.id });
              }}
              onDrop={(e) => {
                e.preventDefault();
                const id = e.dataTransfer.getData("text/plain") || drag?.id;
                const at = drag?.at ?? list.length;
                setDrag(null);
                if (id) void drop(id, c.id, at);
              }}
            >
              <header className="flex items-center gap-2 px-3 pt-3 pb-2">
                <span className={cn("size-2 rounded-full", c.dot)} />
                <h2 className="text-sm font-medium">{c.label}</h2>
                <span className="text-xs text-muted-foreground tabular-nums">{list.length}</span>
              </header>
              <div className="min-h-24 flex-1 space-y-2 overflow-y-auto px-2 pb-3">
                {list.map((s) => {
                  const others = list.filter((x) => x.id !== drag?.id);
                  const k = others.indexOf(s);
                  return (
                    <React.Fragment key={s.id}>
                      {over && drag?.at === k && k >= 0 && <DropLine />}
                      <Card
                        s={s}
                        busy={live[s.id]?.busy ?? s.busy}
                        multi={multi}
                        dragging={drag?.id === s.id}
                        onOpen={() => setOpen(s.id)}
                        onDragStart={(e) => {
                          e.dataTransfer.setData("text/plain", s.id);
                          e.dataTransfer.effectAllowed = "move";
                          setDrag({ id: s.id });
                        }}
                        onDragEnd={() => setDrag(null)}
                      />
                    </React.Fragment>
                  );
                })}
                {over && (drag?.at ?? 0) >= list.filter((x) => x.id !== drag?.id).length && <DropLine />}
                {list.length === 0 && !over && <div className="rounded-lg border border-dashed p-4 text-center text-xs text-muted-foreground">Drop a conversation here</div>}
              </div>
            </section>
          );
        })}
      </div>
      <CardSheet
        s={cards.find((x) => x.id === open)}
        onClose={() => setOpen(null)}
        onMoved={(id, col) => patch(id, { board: col, board_rank: 0 })}
        onOpenConversation={(id) => router.push(`/sessions?id=${id}`)}
      />
    </div>
  );
}

function DropLine() {
  return <div className="h-0.5 rounded-full bg-primary" />;
}

function Card({
  s,
  busy,
  multi,
  dragging,
  onOpen,
  onDragStart,
  onDragEnd,
}: {
  s: Summary;
  busy: boolean;
  multi: boolean;
  dragging: boolean;
  onOpen: () => void;
  onDragStart: (e: React.DragEvent) => void;
  onDragEnd: () => void;
}) {
  return (
    <div
      data-card={s.id}
      role="button"
      tabIndex={0}
      draggable
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onClick={onOpen}
      onKeyDown={(e) => e.key === "Enter" && onOpen()}
      className={cn(
        "cursor-grab space-y-2 rounded-lg border bg-card p-2.5 text-left shadow-xs outline-none hover:border-foreground/20 focus-visible:ring-2 focus-visible:ring-ring/40 active:cursor-grabbing",
        dragging && "opacity-40",
      )}
    >
      <div className="flex items-start gap-1.5">
        <div className="line-clamp-3 min-w-0 flex-1 text-[13px] leading-[18px] font-medium break-words" title={s.title}>
          {s.title || "(untitled)"}
        </div>
        {/* The agent's own state, shown and never confused with the column. */}
        {busy && (
          <span className="mt-1 shrink-0" title="The agent is working on it">
            <Dot on pulse />
          </span>
        )}
      </div>
      {s.origin && <LinkChip link={s.origin} />}
      <div className="flex items-center gap-1.5 text-[11px] whitespace-nowrap text-muted-foreground">
        {s.pinned && <PinIcon className="size-3 shrink-0 rotate-45" aria-label="Pinned" />}
        {s.job && <AlarmClockIcon className="size-3 shrink-0 text-amber-600" aria-label="Scheduled" />}
        {multi && (
          <>
            <span className="min-w-0 truncate" title={s.root}>
              {baseName(s.root)}
            </span>
            <span>·</span>
          </>
        )}
        <span className="flex shrink-0 items-center gap-0.5" title={`${s.messages} messages`}>
          <MessageSquareTextIcon className="size-3" />
          {s.messages}
        </span>
        {!!s.refs && (
          <span className="flex shrink-0 items-center gap-0.5" title={`${s.refs} links`}>
            <LinkIcon className="size-3" />
            {s.refs}
          </span>
        )}
        <span className="ml-auto shrink-0">
          <Ago at={s.updated} />
        </span>
      </div>
    </div>
  );
}

function CardSheet({
  s,
  onClose,
  onMoved,
  onOpenConversation,
}: {
  s?: Summary;
  onClose: () => void;
  onMoved: (id: string, col: BoardColumn) => void;
  onOpenConversation: (id: string) => void;
}) {
  return (
    <Sheet open={!!s} onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-xl">
        {s && (
          <>
            <SheetHeader>
              <SheetTitle className="pr-6 leading-snug">{s.title || "(untitled)"}</SheetTitle>
              <SheetDescription>
                {baseName(s.root)} · {s.engine || "api"} · {s.messages} messages · last <Ago at={s.updated} />
              </SheetDescription>
            </SheetHeader>
            <div className="space-y-5 px-4 pb-6">
              <div className="flex flex-wrap items-center gap-2">
                <BoardPicker key={s.id + (s.board || "")} id={s.id} value={s.board} onMoved={(c) => onMoved(s.id, c)} />
                <Button size="sm" className="ml-auto" onClick={() => onOpenConversation(s.id)}>
                  <MessageSquareTextIcon /> Open conversation
                </Button>
              </div>
              <RefsPanel id={s.id} onJump={(at) => onOpenConversation(`${s.id}#m${at}`)} />
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}
