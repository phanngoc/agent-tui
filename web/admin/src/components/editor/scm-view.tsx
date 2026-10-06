"use client";

import * as React from "react";
import { GitBranchIcon, RefreshCwIcon } from "lucide-react";
import { api, qs } from "@/lib/api";
import { cn } from "@/lib/utils";
import { editorService } from "./service";
import { useEditor } from "./store";
import { IconBtn } from "./explorer-view";

// Source control, as far as reading goes: the branch and the changed files,
// each opening a diff against HEAD whose right side is the file itself,
// editable and saved like any other.

type Change = { path: string; status: string; from?: string };

const statusStyle: Record<string, [string, string]> = {
  M: ["M", "text-amber-600 dark:text-amber-400"],
  A: ["A", "text-emerald-600 dark:text-emerald-400"],
  D: ["D", "text-destructive"],
  R: ["R", "text-sky-600 dark:text-sky-400"],
  "?": ["U", "text-emerald-600 dark:text-emerald-400"],
};

export function letterOf(xy: string): [string, string] {
  const c = xy[0] !== " " && xy[0] !== "?" ? xy[0] : xy[1];
  return statusStyle[c] ?? statusStyle[xy[0]] ?? [c, "text-muted-foreground"];
}

export function useGitStatus() {
  const root = useEditor((s) => s.root);
  const setBranch = useEditor((s) => s.setBranch);
  const [data, setData] = React.useState<{ repo: boolean; branch?: string; changes?: Change[] } | null>(null);
  const load = React.useCallback(async () => {
    if (!root) return;
    try {
      const r = await api.get<{ repo: boolean; branch?: string; changes?: Change[] }>("/api/git/status" + qs({ root }));
      setData(r);
      setBranch(r.branch ? r.branch.split("...")[0] : "");
    } catch {
      setData({ repo: false });
    }
  }, [root, setBranch]);
  return { data, load };
}

export function ScmView() {
  const { data, load } = useGitStatus();
  React.useEffect(() => {
    void load();
    const onFocus = () => void load();
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [load]);
  const staged = (data?.changes ?? []).filter((c) => c.status[0] !== " " && c.status[0] !== "?");
  const unstaged = (data?.changes ?? []).filter((c) => c.status[1] !== " " || c.status[0] === "?");
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center px-3 py-1.5 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
        <span className="flex-1">Source Control</span>
        <IconBtn title="Refresh" onClick={() => void load()}>
          <RefreshCwIcon />
        </IconBtn>
      </div>
      {!data ? (
        <div className="px-3 text-xs text-muted-foreground">…</div>
      ) : !data.repo ? (
        <div className="px-3 text-xs text-muted-foreground">This project is not in a git repository.</div>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto text-[13px]">
          <div className="flex items-center gap-1.5 px-3 pb-2 text-xs text-muted-foreground">
            <GitBranchIcon className="size-3.5" />
            <span className="truncate">{data.branch}</span>
          </div>
          {[
            ["Staged Changes", staged],
            ["Changes", unstaged],
          ].map(([title, list]) =>
            (list as Change[]).length ? (
              <div key={title as string} className="mb-2">
                <div className="px-3 py-0.5 text-[11px] font-semibold text-muted-foreground uppercase">
                  {title as string} · {(list as Change[]).length}
                </div>
                {(list as Change[]).map((c) => {
                  const [l, cls] = letterOf(c.status);
                  const deleted = c.status.includes("D");
                  return (
                    <button
                      key={(title as string) + c.path}
                      onClick={() => (deleted ? undefined : void editorService.openDiff(c.path))}
                      className={cn("flex w-full min-w-0 items-center gap-1.5 px-3 py-0.5 text-left hover:bg-muted/60", deleted && "line-through opacity-70")}
                      title={c.from ? `${c.from} → ${c.path}` : c.path}
                    >
                      <span className="truncate">{c.path.split("/").pop()}</span>
                      <span className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground">{c.path.includes("/") ? c.path.slice(0, c.path.lastIndexOf("/")) : ""}</span>
                      <span className={cn("w-3 shrink-0 text-center font-mono text-[11px] font-semibold", cls)}>{l}</span>
                    </button>
                  );
                })}
              </div>
            ) : null,
          )}
          {!data.changes?.length && <div className="px-3 text-xs text-muted-foreground">No changes.</div>}
        </div>
      )}
    </div>
  );
}
