"use client";

import * as React from "react";
import { ArrowUpIcon, ChevronDownIcon, CpuIcon, FolderIcon, ShieldIcon, SparklesIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import type { EngineInfo, Summary } from "@/lib/types";
import { ErrorNote } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { FolderPicker, describePath, placePath } from "@/components/folder-picker";
import { baseName } from "@/lib/format";

/**
 * sendOnEnter is the chat apps' rule: Enter sends, Shift+Enter is a new line.
 * A key pressed while an input method is composing — Vietnamese, Japanese —
 * belongs to the composition, not to sending.
 */
export function sendOnEnter(e: React.KeyboardEvent, send: () => void) {
  if (e.key !== "Enter" || e.shiftKey || e.nativeEvent.isComposing || e.keyCode === 229) return;
  e.preventDefault();
  send();
}

/** Pill is a compact select that reads as a chip, as in a chat app's composer. */
function Pill({
  icon: Icon,
  value,
  onChange,
  options,
  title,
}: {
  icon: React.ElementType;
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string; disabled?: boolean }[];
  title: string;
}) {
  const label = options.find((o) => o.value === value)?.label ?? value;
  return (
    <label title={title} className="relative inline-flex h-7 cursor-pointer items-center gap-1.5 rounded-full border bg-background px-2.5 text-xs hover:bg-muted">
      <Icon className="size-3.5 text-muted-foreground" />
      <span className="max-w-40 truncate">{label || "default"}</span>
      <ChevronDownIcon className="size-3 text-muted-foreground" />
      <select value={value} onChange={(e) => onChange(e.target.value)} className="absolute inset-0 cursor-pointer opacity-0">
        {options.map((o) => (
          <option key={o.value} value={o.value} disabled={o.disabled}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  );
}

/**
 * NewChat is a conversation that has not started: an empty page with the
 * composer in the middle, ready to type into. Where it runs and on what are
 * chips under the composer, set to where the last conversation was; the
 * session exists from the first message, as in the Claude app.
 */
export function NewChat({ root, onCreated }: { root: string; onCreated: (id: string) => void }) {
  const { data: recent } = useFetch<Summary[]>("/api/sessions" + qs({ root }), [root]);
  if (!recent) return <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">…</div>;
  // The last conversation someone had: a scheduled run or a side chat is not
  // one, and a job's model must not become everyone's default.
  const last = recent.find((s) => !s.job && !s.side_of);
  return <Draft key={last?.id ?? "none"} last={last} fallbackRoot={root} onCreated={onCreated} />;
}

function Draft({ last, fallbackRoot, onCreated }: { last?: Summary; fallbackRoot: string; onCreated: (id: string) => void }) {
  const [dir, setDir] = React.useState(() => (last ? placePath({ root: last.root, target: last.target, cwd: last.cwd }) : fallbackRoot));
  const [engine, setEngine] = React.useState(last?.engine ?? "");
  const [model, setModel] = React.useState(last?.model ?? "");
  const [mode, setMode] = React.useState(last?.mode ?? "");
  const [text, setText] = React.useState("");
  const [picking, setPicking] = React.useState(!last && !fallbackRoot);
  const [err, setErr] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);
  const { data: eng } = useFetch<{ engines: EngineInfo[]; models: { id: string; label: string }[]; modes: string[] }>(
    dir ? "/api/engines" + qs({ root: dir }) : null,
    [dir],
  );
  const where = describePath(dir);

  const start = async () => {
    const prompt = text.trim();
    if (!prompt || busy) return;
    if (!dir) {
      setPicking(true);
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      const r = await api.post<{ id: string }>("/api/sessions", { root: dir, engine, model, mode, prompt });
      onCreated(r.id);
    } catch (e) {
      setErr((e as Error).message);
      setBusy(false);
    }
  };

  const withDefault = (opts: { value: string; label: string; disabled?: boolean }[], label: string) => [{ value: "", label }, ...opts];

  return (
    <div className="flex flex-1 flex-col items-center justify-center overflow-auto px-6 py-10">
      <div className="w-full max-w-3xl">
        <div className="mb-6 text-center">
          <SparklesIcon className="mx-auto mb-2 size-7 text-primary" />
          <h2 className="text-2xl font-semibold tracking-tight">What should we work on?</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {last ? (
              <>Set up like your last conversation, “{last.title}”. Change anything below, or just start typing.</>
            ) : (
              <>Pick a folder, then start typing.</>
            )}
          </p>
        </div>

        <div className={cn("rounded-2xl border bg-background shadow-sm focus-within:ring-[3px] focus-within:ring-ring/30", busy && "opacity-70")}>
          <textarea
            autoFocus
            value={text}
            disabled={busy}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => sendOnEnter(e, () => void start())}
            rows={3}
            placeholder="Ask anything, or describe a task…"
            className="block max-h-[40vh] min-h-24 w-full resize-none rounded-t-2xl bg-transparent px-4 pt-4 pb-2 text-[15px] outline-none placeholder:text-muted-foreground"
          />
          <div className="flex flex-wrap items-center gap-1.5 px-3 pb-3">
            <button
              onClick={() => setPicking(true)}
              title={dir || "Choose a folder"}
              className="inline-flex h-7 max-w-72 items-center gap-1.5 rounded-full border bg-background px-2.5 text-xs hover:bg-muted"
            >
              <FolderIcon className="size-3.5 text-muted-foreground" />
              {where.where && (
                <Badge variant="secondary" className="h-4 px-1 text-[10px] font-normal">
                  {where.where.replace("WSL ", "wsl ")}
                </Badge>
              )}
              <span className="truncate">{dir ? baseName(where.where ? where.dir : dir) || dir : "Choose a folder"}</span>
              <ChevronDownIcon className="size-3 text-muted-foreground" />
            </button>
            <Pill
              icon={CpuIcon}
              title="Engine"
              value={engine}
              onChange={setEngine}
              options={withDefault(
                (eng?.engines ?? []).map((e) => ({ value: e.id, label: e.label + (e.available ? "" : " (unavailable)"), disabled: !e.available })),
                "engine from settings",
              )}
            />
            <Pill icon={SparklesIcon} title="Model" value={model} onChange={setModel} options={withDefault((eng?.models ?? []).map((m) => ({ value: m.id, label: m.label })), "model from settings")} />
            <Pill icon={ShieldIcon} title="Mode: how much the agent may do without asking" value={mode} onChange={setMode} options={withDefault((eng?.modes ?? []).map((m) => ({ value: m, label: m })), "mode from settings")} />
            <Button size="icon" className="ml-auto rounded-full" disabled={busy || !text.trim()} onClick={() => void start()} title="Start (Enter)">
              <ArrowUpIcon />
            </Button>
          </div>
        </div>

        <div className="mt-2 text-center text-xs text-muted-foreground">
          Enter to start · Shift+Enter for a new line ·{" "}
          {where.where ? `runs inside ${where.where}, in ${where.dir}` : "runs in the gateway, with the project's memory, skills and MCP servers"}
        </div>
        <ErrorNote error={err} className="mt-3" />
      </div>
      <FolderPicker open={picking} onOpenChange={setPicking} initial={dir} onPick={setDir} />
    </div>
  );
}
