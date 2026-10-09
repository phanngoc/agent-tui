"use client";

import * as React from "react";
import Link from "next/link";
import { AlarmClockIcon, GripVerticalIcon, LinkIcon, ListIcon, Maximize2Icon, MessageSquareTextIcon, MoreHorizontalIcon, PinIcon, PlusIcon, SearchIcon, XIcon } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useGateway, useVersion } from "@/lib/store";
import type { BoardColumn, Summary } from "@/lib/types";
import { baseName } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Ago, Dot, ErrorNote } from "@/components/common";
import { ProjectSwitcher } from "@/components/project-switcher";
import { BOARD_COLUMNS, BoardPicker, LinkChip } from "@/components/refs-panel";
import { Conversation } from "@/components/conversation";
import { NewChat } from "@/components/new-chat";
import { ContextMenu, type MenuItem } from "@/components/context-menu";
import { Workspace } from "@/components/workspace";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "@/components/ui/sheet";

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

// The columns left to right, the reader's own: kept in this browser, the
// backlog last unless moved.
const DEFAULT_ORDER: BoardColumn[] = ["todo", "doing", "done", "backlog"];
const ORDER_KEY = "agent-tui.board.order";

function parseOrder(raw: string): BoardColumn[] {
  try {
    const v = JSON.parse(raw || "null");
    if (Array.isArray(v) && v.length === DEFAULT_ORDER.length && DEFAULT_ORDER.every((c) => v.includes(c))) return v;
  } catch {}
  return DEFAULT_ORDER;
}

const orderListeners = new Set<() => void>();

// useColumnOrder reads the order as an external store: the prerendered page
// and the first render agree on the default, and another tab's change shows.
function useColumnOrder(): [BoardColumn[], (o: BoardColumn[]) => void] {
  const raw = React.useSyncExternalStore(
    (cb) => {
      orderListeners.add(cb);
      window.addEventListener("storage", cb);
      return () => {
        orderListeners.delete(cb);
        window.removeEventListener("storage", cb);
      };
    },
    () => {
      try {
        return localStorage.getItem(ORDER_KEY) ?? "";
      } catch {
        return "";
      }
    },
    () => "",
  );
  const order = React.useMemo(() => parseOrder(raw), [raw]);
  const set = React.useCallback((o: BoardColumn[]) => {
    try {
      localStorage.setItem(ORDER_KEY, JSON.stringify(o));
    } catch {}
    orderListeners.forEach((f) => f());
  }, []);
  return [order, set];
}

// moveColumn puts column c before (or after) column at.
function moveColumn(order: BoardColumn[], c: BoardColumn, at: BoardColumn, after: boolean): BoardColumn[] {
  if (c === at) return order;
  const rest = order.filter((x) => x !== c);
  const i = rest.indexOf(at) + (after ? 1 : 0);
  return [...rest.slice(0, i), c, ...rest.slice(i)];
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
  const root = useGateway((s) => s.root);
  const live = useGateway((s) => s.live);
  const [all, setAll] = React.useState(false);
  const [q, setQ] = React.useState("");
  const v = useVersion("sessions");
  const { data, error, reload, setData } = useFetch<Summary[]>("/api/sessions" + qs({ root: all ? "" : root, q, archived: "0" }), [v]);
  const [open, setOpen] = React.useState<string | null>(null);
  // newIn is the column a conversation being started from the board lands in.
  const [newIn, setNewIn] = React.useState<BoardColumn | null>(null);
  const created = async (id: string) => {
    const col = newIn;
    setOpen(id);
    setNewIn(null);
    if (col && col !== "backlog") {
      try {
        await api.put(`/api/sessions/${id}/settings`, { board: col });
      } catch (e) {
        toast.error((e as Error).message);
      }
    }
    reload();
  };
  const [drag, setDrag] = React.useState<{ id: string; col?: BoardColumn; at?: number } | null>(null);
  const [colOrder, reorder] = useColumnOrder();
  // A column being dragged by its header, and where it would go.
  const [colDrag, setColDrag] = React.useState<{ id: BoardColumn; at?: BoardColumn; after?: boolean } | null>(null);
  const [colMenu, setColMenu] = React.useState<{ x: number; y: number; id: BoardColumn } | null>(null);

  const shown = colOrder.map((id) => BOARD_COLUMNS.find((c) => c.id === id)!);

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

  // moveAll empties a column into another, a few requests at a time.
  const moveAll = async (from: BoardColumn, to: BoardColumn) => {
    const ids = columns[from].map((s) => s.id);
    const label = (id: BoardColumn) => BOARD_COLUMNS.find((c) => c.id === id)?.label;
    if (!ids.length || !confirm(`Move all ${ids.length} cards from ${label(from)} to ${label(to)}?`)) return;
    setData((d) => d?.map((s) => (ids.includes(s.id) ? { ...s, board: to, board_rank: 0 } : s)));
    let failed = 0;
    for (let i = 0; i < ids.length; i += 8) {
      const res = await Promise.allSettled(ids.slice(i, i + 8).map((id) => api.put(`/api/sessions/${id}/settings`, { board: to, board_rank: 0 })));
      failed += res.filter((r) => r.status === "rejected").length;
    }
    if (failed) {
      toast.error(`${failed} of ${ids.length} could not be moved`);
      reload();
    } else toast.success(`${ids.length} cards → ${label(to)}`);
  };

  const menuFor = (id: BoardColumn): MenuItem[] => {
    const i = colOrder.indexOf(id);
    return [
      { label: "Move column left", disabled: i === 0, run: () => reorder(moveColumn(colOrder, id, colOrder[i - 1], false)) },
      { label: "Move column right", disabled: i === colOrder.length - 1, run: () => reorder(moveColumn(colOrder, id, colOrder[i + 1], true)) },
      "-",
      {
        label: `Move all ${columns[id].length} cards to`,
        disabled: columns[id].length === 0,
        items: BOARD_COLUMNS.filter((c) => c.id !== id).map((c) => ({ label: c.label, run: () => void moveAll(id, c.id) })),
      },
      "-",
      { label: "Reset column order", disabled: colOrder.join() === DEFAULT_ORDER.join(), run: () => reorder(DEFAULT_ORDER) },
    ];
  };

  const multi = all || !root;
  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* One bar, title to actions: the room goes to the columns. */}
      <div className="flex flex-wrap items-center gap-2 border-b px-4 py-2">
        <h1
          className="mr-1 text-base font-semibold"
          title="Your conversations as tasks. Drag a card between columns, or open it to read and answer it here. A card stays where you put it, whatever its agent is doing."
        >
          Board
        </h1>
        <div className="relative w-56 max-w-full">
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
        <span className="ml-auto text-xs text-muted-foreground" title="Archived conversations are left out">
          {cards.length} conversation{cards.length === 1 ? "" : "s"}
        </span>
        <Link href="/sessions" className={buttonVariants({ variant: "ghost", size: "sm" })}>
          <ListIcon /> List view
        </Link>
        <Button size="sm" onClick={() => setNewIn("todo")} title="Start a conversation here, as a card in Todo">
          <PlusIcon /> New conversation
        </Button>
      </div>
      <ErrorNote error={error} className="m-3" />
      <div className="grid min-h-0 flex-1 grid-cols-[repeat(4,minmax(260px,1fr))] gap-3 overflow-auto p-3">
        {shown.map((c) => {
          const list = columns[c.id];
          const over = drag?.col === c.id;
          const colOver = colDrag && colDrag.id !== c.id && colDrag.at === c.id;
          return (
            <section
              key={c.id}
              aria-label={c.label}
              className={cn(
                "relative flex min-h-0 flex-col rounded-xl border bg-muted/30",
                over && "border-primary/60 bg-primary/5",
                colDrag?.id === c.id && "opacity-50",
              )}
              onDragOver={(e) => {
                if (colDrag) {
                  // A column dragged over another: before or after it, by which half the pointer is in.
                  e.preventDefault();
                  e.dataTransfer.dropEffect = "move";
                  const r = e.currentTarget.getBoundingClientRect();
                  const after = e.clientX > r.left + r.width / 2;
                  if (colDrag.at !== c.id || colDrag.after !== after) setColDrag({ ...colDrag, at: c.id, after });
                  return;
                }
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
                if (colDrag) {
                  if (colDrag.at) reorder(moveColumn(colOrder, colDrag.id, colDrag.at, !!colDrag.after));
                  setColDrag(null);
                  return;
                }
                const id = e.dataTransfer.getData("text/plain") || drag?.id;
                const at = drag?.at ?? list.length;
                setDrag(null);
                if (id) void drop(id, c.id, at);
              }}
            >
              {colOver && <div className={cn("absolute inset-y-2 w-1 rounded-full bg-primary", colDrag?.after ? "-right-2" : "-left-2")} />}
              {/* The header is the column's handle: drag it to put the column somewhere else. */}
              <header
                draggable
                onDragStart={(e) => {
                  e.dataTransfer.setData("application/x-board-column", c.id);
                  e.dataTransfer.effectAllowed = "move";
                  setColDrag({ id: c.id });
                }}
                onDragEnd={() => setColDrag(null)}
                title="Drag to move the column"
                className="group/col flex cursor-grab items-center gap-2 px-3 pt-3 pb-2 active:cursor-grabbing"
              >
                <GripVerticalIcon className="-ml-1 size-3.5 text-muted-foreground opacity-40 group-hover/col:opacity-100" />
                <span className={cn("size-2 rounded-full", c.dot)} />
                <h2 className="text-sm font-medium">{c.label}</h2>
                <span className="text-xs text-muted-foreground tabular-nums">{list.length}</span>
                <button
                  aria-label={`New conversation in ${c.label}`}
                  title={`New conversation in ${c.label}`}
                  onClick={() => setNewIn(c.id)}
                  className="ml-auto grid size-6 place-items-center rounded-md text-muted-foreground hover:bg-background hover:text-foreground"
                >
                  <PlusIcon className="size-4" />
                </button>
                <button
                  aria-label={`${c.label} column actions`}
                  title="Column actions"
                  onClick={(e) => {
                    const r = e.currentTarget.getBoundingClientRect();
                    setColMenu({ x: r.left, y: r.bottom + 2, id: c.id });
                  }}
                  className="grid size-6 place-items-center rounded-md text-muted-foreground hover:bg-background hover:text-foreground"
                >
                  <MoreHorizontalIcon className="size-4" />
                </button>
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
      {colMenu && <ContextMenu x={colMenu.x} y={colMenu.y} items={menuFor(colMenu.id)} onClose={() => setColMenu(null)} />}
      <CardSheet
        id={open}
        s={cards.find((x) => x.id === open)}
        newIn={newIn}
        root={root}
        onCreated={(id) => void created(id)}
        onClose={() => {
          setOpen(null);
          setNewIn(null);
        }}
        onMoved={(id, col) => patch(id, { board: col, board_rank: 0 })}
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

/**
 * CardSheet opens a card as its conversation, whole — the transcript as it
 * streams, the composer, approvals and questions, the side panel with its
 * references — so a task is read, answered and moved without leaving the
 * board. Opened with newIn and no id, it starts a conversation instead, and
 * becomes that conversation once it is made.
 */
function CardSheet({
  id,
  s,
  newIn,
  root,
  onCreated,
  onClose,
  onMoved,
}: {
  id: string | null;
  s?: Summary;
  newIn: BoardColumn | null;
  root: string;
  onCreated: (id: string) => void;
  onClose: () => void;
  onMoved: (id: string, col: BoardColumn) => void;
}) {
  const label = BOARD_COLUMNS.find((c) => c.id === newIn)?.label;
  return (
    <Sheet
      open={!!id || !!newIn}
      onOpenChange={(o, details) => {
        if (o) return;
        // Escape while typing is the composer's (its menu, its side chat), not a reason to leave.
        const el = document.activeElement;
        if (details?.reason === "escape-key" && el && (el.tagName === "TEXTAREA" || el.tagName === "INPUT")) return;
        onClose();
      }}
    >
      <SheetContent showCloseButton={false} className="w-full gap-0 p-0 data-[side=right]:w-[min(1500px,96vw)] data-[side=right]:sm:max-w-none">
        <SheetTitle className="sr-only">{id ? s?.title || "(untitled)" : `New conversation in ${label}`}</SheetTitle>
        <SheetDescription className="sr-only">The conversation, to read and answer here, and its place on the board.</SheetDescription>
        <div className="flex shrink-0 flex-wrap items-center gap-2 border-b px-3 py-2">
          {id ? (
            <>
              <BoardPicker key={id + (s?.board || "")} id={id} value={s?.board} onMoved={(c) => onMoved(id, c)} />
              {s && (
                <span className="truncate text-xs text-muted-foreground">
                  {baseName(s.root)} · {s.messages} messages
                </span>
              )}
            </>
          ) : (
            <span className="text-sm font-medium">
              New conversation <span className="font-normal text-muted-foreground">· lands in {label}</span>
            </span>
          )}
          <div className="ml-auto flex items-center gap-1">
            {id && (
              <Link href={`/sessions?id=${id}`} className={buttonVariants({ variant: "ghost", size: "sm" })} title="Open it on the sessions page">
                <Maximize2Icon /> Full page
              </Link>
            )}
            <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close" title="Close (Esc)">
              <XIcon />
            </Button>
          </div>
        </div>
        <div className="flex min-h-0 flex-1 flex-col">
          {id ? (
            <Workspace id="board-chat" right={{ node: null, defaultSize: 380, minSize: 300, maxSize: 640, foldBelow: 1200, label: "side panel" }}>
              <Conversation key={id} id={id} initialTab="refs" />
            </Workspace>
          ) : (
            newIn && <NewChat key={newIn + root} root={root} onCreated={onCreated} />
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
