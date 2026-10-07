"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { ArchiveIcon, MoreHorizontalIcon, PinIcon, PlusIcon, SearchIcon } from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useGateway, useVersion } from "@/lib/store";
import { isUnread, useUnread } from "@/lib/unread";
import type { Summary } from "@/lib/types";
import { Ago, Dot, ErrorNote } from "@/components/common";
import { ProjectSwitcher } from "@/components/project-switcher";
import { ContextMenu, type MenuItem } from "@/components/context-menu";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { baseName } from "@/lib/format";

/** setListSettings changes what the list's menu changes: the name, the pin, archived. */
export async function setListSettings(id: string, body: { title?: string; pinned?: boolean; archived?: boolean; chapter?: { at: number; on: boolean } }) {
  await api.put(`/api/sessions/${id}/settings`, body);
}

/**
 * SessionList is the list of conversations, with Claude's right-click menu on
 * each: open in, pin, mark as unread, rename, fork, archive, delete. The ⋯ on
 * a row opens the same menu, for a phone or a trackpad without a right click.
 */
export function SessionList({ selected, onSelect, onNew }: { selected: string; onSelect: (id: string) => void; onNew: () => void }) {
  const router = useRouter();
  const root = useGateway((s) => s.root);
  const live = useGateway((s) => s.live);
  const [all, setAll] = React.useState(false);
  const [archived, setArchived] = React.useState(false);
  const [q, setQ] = React.useState("");
  const v = useVersion("sessions");
  const { data, error, reload, setData } = useFetch<Summary[]>("/api/sessions" + qs({ root: all ? "" : root, q, archived: archived ? "1" : "0" }), [v]);
  const [menu, setMenu] = React.useState<{ x: number; y: number; s: Summary } | null>(null);
  const [renaming, setRenaming] = React.useState("");
  const unread = useUnread();

  // Pinned first, then as the gateway sorts them: the latest on top.
  const rows = React.useMemo(() => {
    const list = (data ?? []).filter((s) => !s.side_of);
    return [...list.filter((s) => s.pinned), ...list.filter((s) => !s.pinned)];
  }, [data]);

  React.useEffect(() => {
    if (data) useUnread.getState().know(data);
  }, [data]);
  // The conversation on screen is read, as it changes too; marked unread by
  // hand, it stays so until it is left and opened again.
  const sel = rows.find((s) => s.id === selected);
  const selUpdated = sel?.updated;
  const lastSel = React.useRef("");
  React.useEffect(() => {
    if (!selected || !selUpdated) return;
    const st = useUnread.getState();
    if (lastSel.current !== selected) {
      lastSel.current = selected;
      st.read(selected, selUpdated);
    } else if (!st.marked[selected]) st.read(selected, selUpdated);
  }, [selected, selUpdated]);

  const patch = (id: string, p: Partial<Summary>) => setData((d) => d?.map((s) => (s.id === id ? { ...s, ...p } : s)));
  const drop = (id: string) => setData((d) => d?.filter((s) => s.id !== id));
  const act = async (f: () => Promise<unknown>, undo?: () => void) => {
    try {
      await f();
    } catch (e) {
      undo?.();
      toast.error((e as Error).message);
    } finally {
      // A terminal holding the session applies it a moment later.
      setTimeout(reload, 400);
    }
  };

  const rename = (s: Summary, title: string) => {
    setRenaming("");
    title = title.trim().replace(/\s+/g, " ");
    if (title === s.title) return;
    patch(s.id, { title: title || "new session" });
    void act(() => setListSettings(s.id, { title }), () => patch(s.id, { title: s.title }));
  };
  const remove = (s: Summary) => {
    drop(s.id);
    if (selected === s.id) router.push("/sessions");
    void act(async () => {
      await api.del(`/api/sessions/${s.id}`);
      toast.success(`Deleted «${s.title || "untitled"}»`, {
        action: {
          label: "Undo",
          onClick: () => void act(() => api.post(`/api/sessions/${s.id}/restore`, {})),
        },
        duration: 8000,
      });
    }, reload);
  };
  const archive = (s: Summary, on: boolean) => {
    drop(s.id);
    void act(async () => {
      await setListSettings(s.id, { archived: on });
      toast.success(on ? `Archived «${s.title || "untitled"}»` : `Restored «${s.title || "untitled"}» to the list`, {
        action: on ? { label: "Undo", onClick: () => void act(() => setListSettings(s.id, { archived: false })) } : undefined,
      });
    }, reload);
  };
  const fork = (s: Summary) =>
    void act(async () => {
      const r = await api.post<{ id: string }>(`/api/sessions/${s.id}/fork`, {});
      toast.success(`Forked «${s.title || "untitled"}»`, { description: "The agent branches on your next message." });
      onSelect(r.id);
    });

  const itemsFor = (s: Summary): MenuItem[] => {
    const un = isUnread(unread, s);
    return [
      {
        label: "Open in",
        items: [
          { label: "New Tab", run: () => window.open(`/sessions?id=${s.id}`, "_blank") },
          {
            label: "Editor",
            run: () => {
              useGateway.getState().setRoot(s.root);
              router.push("/editor?root=" + encodeURIComponent(s.root));
            },
          },
        ],
      },
      "-",
      s.pinned
        ? { label: "Unpin", hint: "P", run: () => (patch(s.id, { pinned: false }), void act(() => setListSettings(s.id, { pinned: false }))) }
        : { label: "Pin", hint: "P", run: () => (patch(s.id, { pinned: true }), void act(() => setListSettings(s.id, { pinned: true }))) },
      un
        ? { label: "Mark as read", hint: "U", run: () => unread.read(s.id, s.updated) }
        : { label: "Mark as unread", hint: "U", run: () => unread.markUnread(s.id) },
      { label: "Rename", hint: "R", run: () => setRenaming(s.id) },
      { label: "Fork", hint: "F", run: () => fork(s), disabled: s.messages === 0 },
      { label: "Copy Link", run: () => void navigator.clipboard.writeText(location.origin + `/sessions?id=${s.id}`).then(() => toast.success("Link copied")) },
      "-",
      s.closed ? { label: "Unarchive", hint: "A", run: () => archive(s, false) } : { label: "Archive", hint: "A", run: () => archive(s, true) },
      { label: "Delete", hint: "D", danger: true, run: () => remove(s) },
    ];
  };

  const pinned = rows.filter((s) => s.pinned).length;
  return (
    <div className="flex h-full min-w-0 flex-col">
      <div className="space-y-2 border-b p-3">
        <div className="flex items-center gap-2">
          <div className="relative flex-1">
            <SearchIcon className="absolute top-2 left-2 size-4 text-muted-foreground" />
            <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search sessions" className="pl-8" />
          </div>
          <Button size="icon" onClick={onNew} title="New conversation">
            <PlusIcon />
          </Button>
        </div>
        <div className="flex items-center gap-1 text-xs">
          {/* The project in view, picked here: there is no bar across the top any more. */}
          <ProjectSwitcher variant="pill" className={cn(root && !all ? "bg-muted" : "font-normal text-muted-foreground")} onPicked={() => setAll(false)} />
          {root && (
            <button className={cn("shrink-0 rounded px-2 py-0.5", all ? "bg-muted font-medium" : "text-muted-foreground")} onClick={() => setAll(!all)}>
              All projects
            </button>
          )}
          <button
            className={cn("flex shrink-0 items-center gap-1 rounded px-2 py-0.5", archived ? "bg-muted font-medium" : "text-muted-foreground")}
            onClick={() => setArchived(!archived)}
            title={archived ? "Back to the conversations" : "Show archived conversations"}
          >
            <ArchiveIcon className="size-3" />
            Archived
          </button>
          <span className="ml-auto text-muted-foreground">{rows.length}</span>
        </div>
      </div>
      <ErrorNote error={error} className="m-3" />
      <div className="min-h-0 flex-1 overflow-auto">
        {archived && rows.length === 0 && data && <div className="p-4 text-sm text-muted-foreground">No archived conversations.</div>}
        {rows.map((s, i) => {
          const l = live[s.id];
          const busy = l?.busy ?? s.busy;
          const waiting = l && Object.keys(l.approvals).length + Object.keys(l.choices).length > 0;
          const un = selected !== s.id && isUnread(unread, { ...s, busy });
          const open = (e: React.MouseEvent, at?: DOMRect) => {
            e.preventDefault();
            e.stopPropagation();
            setMenu({ x: at ? at.left : e.clientX, y: at ? at.bottom + 2 : e.clientY, s });
          };
          return (
            <React.Fragment key={s.id}>
              {pinned > 0 && (i === 0 || i === pinned) && (
                <div className="px-3 pt-3 pb-1 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{i === 0 ? "Pinned" : "Recents"}</div>
              )}
              <div
                role="button"
                tabIndex={0}
                data-session={s.id}
                onClick={() => renaming !== s.id && onSelect(s.id)}
                onKeyDown={(e) => {
                  if (renaming === s.id) return;
                  if (e.key === "Enter") onSelect(s.id);
                  if (e.key === "F2") setRenaming(s.id);
                }}
                onContextMenu={open}
                className={cn(
                  "group relative flex w-full cursor-pointer flex-col gap-1 border-b px-3 py-2.5 text-left outline-none hover:bg-muted/50 focus-visible:bg-muted/50",
                  (selected === s.id || menu?.s.id === s.id) && "bg-muted",
                )}
              >
                <div className="flex items-center gap-2">
                  {un ? <span className="size-2 shrink-0 rounded-full bg-amber-500" title="Unread" /> : <Dot on={busy} pulse />}
                  {renaming === s.id ? (
                    <RenameInput initial={s.title} onDone={(t) => (t === null ? setRenaming("") : rename(s, t))} />
                  ) : (
                    <span className={cn("min-w-0 flex-1 truncate text-sm", un ? "font-semibold" : "font-medium")}>{s.title || "(untitled)"}</span>
                  )}
                  {s.pinned && renaming !== s.id && <PinIcon className="size-3 shrink-0 rotate-45 text-muted-foreground" />}
                  {waiting && <span className="rounded bg-amber-500/15 px-1 text-[10px] font-medium text-amber-700 dark:text-amber-300">needs you</span>}
                  {renaming !== s.id && (
                    <button
                      aria-label="More"
                      title="More"
                      onClick={(e) => open(e, e.currentTarget.getBoundingClientRect())}
                      className={cn(
                        "grid size-6 shrink-0 place-items-center rounded text-muted-foreground hover:bg-background hover:text-foreground md:opacity-0 md:group-hover:opacity-100 md:focus-visible:opacity-100",
                        menu?.s.id === s.id && "md:opacity-100",
                      )}
                    >
                      <MoreHorizontalIcon className="size-4" />
                    </button>
                  )}
                </div>
                <div className="flex items-center gap-2 pl-4 text-xs text-muted-foreground">
                  <span className="truncate">{baseName(s.root)}</span>
                  <span>·</span>
                  {s.job && <span className="rounded bg-amber-500/15 px-1 text-amber-700 dark:text-amber-300">scheduled</span>}
                  <span>{s.engine || "api"}</span>
                  <span>·</span>
                  <span>{s.messages} msg</span>
                  <span className="ml-auto">
                    <Ago at={s.updated} />
                  </span>
                </div>
                {s.owner?.startsWith("tui") && <div className="pl-4 text-[11px] text-primary">open in a terminal</div>}
              </div>
            </React.Fragment>
          );
        })}
      </div>
      {menu && <ContextMenu x={menu.x} y={menu.y} items={itemsFor(menu.s)} onClose={() => setMenu(null)} />}
    </div>
  );
}

/** RenameInput edits a name in place: Enter or leaving it saves, Escape keeps the old one. */
export function RenameInput({ initial, onDone, className }: { initial: string; onDone: (title: string | null) => void; className?: string }) {
  const [v, setV] = React.useState(initial);
  const done = React.useRef(false);
  const finish = (t: string | null) => {
    if (done.current) return;
    done.current = true;
    onDone(t);
  };
  return (
    <input
      autoFocus
      value={v}
      maxLength={200}
      aria-label="Name"
      onFocus={(e) => e.currentTarget.select()}
      onChange={(e) => setV(e.target.value)}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Enter") finish(v);
        if (e.key === "Escape") finish(null);
      }}
      onBlur={() => finish(v)}
      className={cn("min-w-0 flex-1 rounded border border-primary bg-background px-1.5 py-0.5 text-sm font-medium outline-none", className)}
    />
  );
}
