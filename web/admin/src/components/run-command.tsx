"use client";

import * as React from "react";
import { CheckIcon, Loader2Icon, PlayIcon, RotateCwIcon, SquareIcon, XIcon } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { cn } from "@/lib/utils";
import { guessShell, type RunShell } from "@/lib/run";
import { TermView } from "@/components/terminal-panel";

// Running a command an answer suggests, from beside it, as the Claude app
// does: a Run button on the command, and its output streaming into a box
// under it. The command runs in a real terminal the gateway holds — the
// project's own shell, or one on the Windows side for a project in WSL — so
// colour, progress bars and a prompt for input all work, and the box takes
// keystrokes.

/** RunRoot is the project commands run in; without one there is no Run. */
export const RunRoot = React.createContext("");

const shellLists = new Map<string, Promise<RunShell[]>>();

/** useRunShells lists the shells a project can run a command in. */
function useRunShells(root: string): RunShell[] | null {
  const [shells, setShells] = React.useState<RunShell[] | null>(null);
  React.useEffect(() => {
    if (!root) return;
    let live = true;
    let list = shellLists.get(root);
    if (!list) {
      list = api.get<{ shells: RunShell[] }>("/api/term/shells" + qs({ root, run: "1" })).then((r) => r.shells);
      list.catch(() => shellLists.delete(root));
      shellLists.set(root, list);
    }
    list.then(
      (s) => live && setShells(s),
      (e: Error) => {
        if (live) setShells([]);
        toast.error(e.message);
      },
    );
    return () => {
      live = false;
    };
  }, [root]);
  return shells;
}

/** RunButton is the small Run on a command. */
export function RunButton({ onClick, className, label = true }: { onClick: () => void; className?: string; label?: boolean }) {
  return (
    <button
      type="button"
      title="Run this command"
      aria-label="Run this command"
      onClick={(e) => {
        e.preventDefault();
        e.stopPropagation();
        onClick();
      }}
      className={cn(
        "inline-flex items-center gap-1 rounded-md border border-emerald-500/40 bg-emerald-500/10 px-1.5 py-0.5 align-middle font-sans text-[11px] leading-none font-medium text-emerald-700 not-italic hover:bg-emerald-500/20 dark:text-emerald-300",
        className,
      )}
    >
      <PlayIcon className="size-2.5 fill-current" />
      {label && "Run"}
    </button>
  );
}

type State = { phase: "starting" } | { phase: "running"; id: string } | { phase: "done"; id: string; code: number } | { phase: "failed"; error: string };

/**
 * RunBox runs a command as soon as it is shown, in the shell guessed for it,
 * and streams what it prints. The shell can be changed and the command run
 * again; Stop sends Ctrl+C; closing ends the shell.
 */
export function RunBox({ command, lang, context, onClose }: { command: string; lang?: string; context?: string; onClose: () => void }) {
  const root = React.useContext(RunRoot);
  const shells = useRunShells(root);
  // The shell chosen in the box, or else the one guessed for the command.
  const [picked, setPicked] = React.useState<string | null>(null);
  const shell = picked ?? (shells ? guessShell(command, shells, lang, context) : "");
  const [state, setState] = React.useState<State>({ phase: "starting" });
  const current = React.useRef<string | null>(null);

  // launch starts the command in a shell; the box shows "starting" until it
  // answers.
  const launch = React.useCallback(
    async (sh: string) => {
      if (current.current) void api.del(`/api/term/${current.current}`).catch(() => undefined);
      current.current = null;
      try {
        const t = await api.post<{ id: string }>("/api/term", { root, shell: sh, command, cols: 120, rows: 16 });
        current.current = t.id;
        setState({ phase: "running", id: t.id });
      } catch (e) {
        setState({ phase: "failed", error: (e as Error).message });
      }
    },
    [root, command],
  );

  const start = (sh: string) => {
    setState({ phase: "starting" });
    void launch(sh);
  };

  // Run once the shells are known: a click on Run is the go-ahead.
  const started = React.useRef(false);
  React.useEffect(() => {
    if (!shell || started.current) return;
    started.current = true;
    void launch(shell);
  }, [shell, launch]);

  // The shell ends with the box.
  React.useEffect(
    () => () => {
      if (current.current) void api.del(`/api/term/${current.current}`).catch(() => undefined);
    },
    [],
  );

  const id = state.phase === "running" || state.phase === "done" ? state.id : null;
  const onExit = React.useCallback((code: number) => setState((s) => (s.phase === "running" ? { phase: "done", id: s.id, code } : s)), []);
  const label = shells?.find((s) => s.id === shell)?.label ?? "";

  return (
    <div className="not-prose my-2 overflow-hidden rounded-lg border bg-background text-xs shadow-sm">
      <div className="flex items-center gap-2 border-b bg-muted/40 px-2 py-1">
        {state.phase === "running" || state.phase === "starting" ? (
          <Loader2Icon className="size-3.5 shrink-0 animate-spin text-sky-600" />
        ) : state.phase === "done" && state.code === 0 ? (
          <CheckIcon className="size-3.5 shrink-0 text-emerald-600" />
        ) : (
          <XIcon className="size-3.5 shrink-0 text-destructive" />
        )}
        <code className="min-w-0 flex-1 truncate font-mono" title={command}>
          {command.split("\n")[0]}
          {command.includes("\n") && " …"}
        </code>
        <span
          className={cn(
            "shrink-0 tabular-nums",
            state.phase === "done" && state.code !== 0 && "text-destructive",
            state.phase === "done" && state.code === 0 && "text-emerald-700 dark:text-emerald-400",
          )}
        >
          {state.phase === "starting" ? "starting…" : state.phase === "running" ? "running" : state.phase === "done" ? `exit ${state.code}` : "failed"}
        </span>
        {shells && shells.length > 1 ? (
          <select
            value={shell}
            onChange={(e) => {
              setPicked(e.target.value);
              start(e.target.value);
            }}
            title="Where it runs: changing it runs the command again there"
            aria-label="Shell"
            className="max-w-44 shrink-0 truncate rounded border bg-background px-1 py-0.5 text-[11px]"
          >
            {shells.map((s) => (
              <option key={s.id} value={s.id}>
                {s.label}
              </option>
            ))}
          </select>
        ) : (
          label && <span className="shrink-0 text-muted-foreground">{label}</span>
        )}
        {state.phase === "running" ? (
          <button
            type="button"
            title="Stop (Ctrl+C)"
            onClick={() => void api.post(`/api/term/${state.id}/input`, { data: "\x03" }).catch(() => undefined)}
            className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <SquareIcon className="size-3" />
          </button>
        ) : (
          <button type="button" title="Run again" disabled={!shell} onClick={() => start(shell)} className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground">
            <RotateCwIcon className="size-3" />
          </button>
        )}
        <button type="button" title="Close (ends the command if it still runs)" onClick={onClose} className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground">
          <XIcon className="size-3" />
        </button>
      </div>
      {shells && !shell ? (
        <div className="px-3 py-2 text-destructive">No shell here can run a command.</div>
      ) : state.phase === "failed" ? (
        <div className="px-3 py-2 text-destructive">{state.error}</div>
      ) : (
        <div className="relative h-44 resize-y overflow-hidden">{id && <TermView key={id} id={id} active onExit={onExit} />}</div>
      )}
    </div>
  );
}
