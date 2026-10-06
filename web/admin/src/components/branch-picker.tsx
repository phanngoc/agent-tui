"use client";

import * as React from "react";
import {
  CheckIcon,
  ChevronDownIcon,
  GitBranchIcon,
  PlusIcon,
} from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { cn } from "@/lib/utils";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";

export interface Branches {
  current: string;
  detached?: boolean;
  local: string[];
  remote?: string[];
  dirty: number;
  top: string;
  worktree?: boolean;
}

/**
 * BranchPicker is the branch chip under a composer, as in the Claude app.
 *
 * For a conversation (session) or a project (root), it shows the branch the
 * working tree is on and switches it. With `onBase` it switches nothing: it
 * picks the branch a new worktree starts from.
 */
export function BranchPicker({
  session,
  root,
  base,
  onBase,
  disabled,
  version = "",
  className,
}: {
  session?: string;
  root?: string;
  base?: string;
  onBase?: (branch: string) => void;
  disabled?: boolean;
  version?: string | number;
  className?: string;
}) {
  const path =
    session || root
      ? "/api/git/branches" + qs({ session, root: session ? undefined : root })
      : null;
  const { data, error, reload } = useFetch<Branches>(path, [version]);
  const [open, setOpen] = React.useState(false);
  const [filter, setFilter] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  if (error || !data) return null; // not a repository, or not yet known
  const picking = !!onBase;
  const shown = picking ? base || data.current : data.current;
  const f = filter.trim().toLowerCase();
  const local = data.local.filter((b) => b.toLowerCase().includes(f));
  const remote = (data.remote ?? []).filter(
    (b) =>
      b.toLowerCase().includes(f) &&
      !data.local.includes(b.slice(b.indexOf("/") + 1)),
  );
  const exact =
    data.local.includes(filter.trim()) ||
    (data.remote ?? []).includes(filter.trim());

  const pick = async (branch: string, create = false) => {
    if (picking) {
      onBase?.(branch);
      setOpen(false);
      return;
    }
    if (branch === data.current && !create) {
      setOpen(false);
      return;
    }
    setBusy(true);
    try {
      await api.post("/api/git/switch", {
        session,
        root: session ? undefined : root,
        branch,
        create,
      });
      toast.success(
        create ? `On a new branch, ${branch}` : `Switched to ${branch}`,
      );
      setOpen(false);
      setFilter("");
      reload();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const row = (b: string, current: boolean) => (
    <button
      key={b}
      type="button"
      disabled={busy}
      onClick={() => void pick(b)}
      className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] hover:bg-muted disabled:opacity-50"
    >
      <CheckIcon
        className={cn(
          "size-3.5 shrink-0",
          current ? "opacity-100" : "opacity-0",
        )}
      />
      <span className="truncate font-mono">{b}</span>
    </button>
  );

  return (
    <Popover
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) reload();
      }}
    >
      <PopoverTrigger
        disabled={disabled}
        title={
          picking
            ? "The branch the new worktree starts from"
            : data.dirty
              ? `${data.dirty} changed file${data.dirty > 1 ? "s" : ""} in ${data.top}`
              : `Working tree: ${data.top}`
        }
        className={cn(
          "inline-flex h-7 max-w-56 items-center gap-1.5 rounded-full border bg-background px-2.5 text-xs hover:bg-muted disabled:opacity-50",
          className,
        )}
      >
        <GitBranchIcon className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="truncate font-mono">{shown}</span>
        {!picking && data.dirty > 0 && (
          <span className="size-1.5 shrink-0 rounded-full bg-amber-500" />
        )}
        {!picking && data.worktree && (
          <span className="rounded bg-muted px-1 text-[10px] text-muted-foreground">
            worktree
          </span>
        )}
        <ChevronDownIcon className="size-3 shrink-0 text-muted-foreground" />
      </PopoverTrigger>
      <PopoverContent align="start" side="top" className="w-80 p-1.5">
        <input
          autoFocus
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && filter.trim()) {
              e.preventDefault();
              const first = local[0] ?? remote[0];
              if (
                exact ||
                (first && !filter.includes(" ") && first.toLowerCase() === f)
              )
                void pick(first ?? filter.trim());
              else if (!picking) void pick(filter.trim(), true);
              else if (first) void pick(first);
            }
          }}
          placeholder={
            picking
              ? "Start the worktree from…"
              : "Switch branch, or name a new one"
          }
          className="mb-1 h-8 w-full rounded-md border bg-transparent px-2 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        />
        <div className="max-h-72 overflow-auto">
          {local.map((b) => row(b, b === shown))}
          {remote.length > 0 && (
            <>
              <div className="px-2 pt-2 pb-1 text-[11px] font-medium text-muted-foreground uppercase">
                Remote
              </div>
              {remote.slice(0, 30).map((b) => row(b, false))}
            </>
          )}
          {local.length === 0 && remote.length === 0 && !filter && (
            <div className="px-2 py-1.5 text-xs text-muted-foreground">
              No branches.
            </div>
          )}
        </div>
        {!picking && filter.trim() && !exact && (
          <button
            type="button"
            disabled={busy}
            onClick={() => void pick(filter.trim(), true)}
            className="mt-1 flex w-full items-center gap-2 rounded-md border-t px-2 py-1.5 text-left text-[13px] hover:bg-muted"
          >
            <PlusIcon className="size-3.5" /> Create branch{" "}
            <span className="truncate font-mono">{filter.trim()}</span> from{" "}
            {data.current}
          </button>
        )}
        {!picking && data.dirty > 0 && (
          <div className="mt-1 border-t px-2 pt-1.5 text-[11px] text-muted-foreground">
            {data.dirty} uncommitted change{data.dirty > 1 ? "s" : ""}: git
            refuses a switch that would overwrite them.
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
