"use client";

import * as React from "react";
import { ChevronDownIcon } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import type { EngineInfo } from "@/lib/types";
import { cn } from "@/lib/utils";

// What a session runs on — engine, model, mode — as chips that change it, from
// its next turn, wherever it is held: a terminal holding it applies the change
// as if it had been chosen there.

function Chip({
  value,
  options,
  onChange,
  title,
  className,
}: {
  value: string;
  options: { value: string; label: string; disabled?: boolean }[];
  onChange: (v: string) => void;
  title: string;
  className?: string;
}) {
  const known = options.some((o) => o.value === value);
  return (
    <label
      title={title}
      className={cn(
        "relative inline-flex cursor-pointer items-center gap-1 rounded-full border px-2 py-0.5 text-xs hover:bg-muted/60 focus-within:ring-2 focus-within:ring-ring/40",
        className,
      )}
    >
      <span className="max-w-48 truncate">{options.find((o) => o.value === value)?.label ?? (value || "default")}</span>
      <ChevronDownIcon className="size-3 opacity-60" />
      <select
        value={known ? value : ""}
        onChange={(e) => e.target.value && onChange(e.target.value)}
        className="absolute inset-0 cursor-pointer opacity-0"
        aria-label={title}
      >
        {!known && <option value="">{value || "default"}</option>}
        {options.map((o) => (
          <option key={o.value} value={o.value} disabled={o.disabled}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  );
}

export function SessionSettings({
  id,
  root,
  engine,
  model,
  mode,
  onChanged,
}: {
  id: string;
  root: string;
  engine?: string;
  model?: string;
  mode?: string;
  onChanged?: (patch: { engine?: string; model?: string; mode?: string }) => void;
}) {
  const { data } = useFetch<{ engines: EngineInfo[]; models: { id: string; label: string }[]; modes: string[] }>("/api/engines" + qs({ root }), [root]);
  const set = async (patch: { engine?: string; model?: string; mode?: string }, what: string) => {
    try {
      await api.put(`/api/sessions/${id}/settings`, patch);
      onChanged?.(patch);
      toast.success(`${what} from the next turn`);
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  const engines = (data?.engines ?? []).map((e) => ({ value: e.id, label: e.label || e.id, disabled: !e.available }));
  const models = (data?.models ?? []).map((m) => ({ value: m.id, label: m.label }));
  const modes = (data?.modes ?? ["plan", "ask", "auto", "full"]).map((m) => ({ value: m, label: `mode ${m}` }));
  const ownModel = !engine || engine === "api" || engine === "claude";
  return (
    <>
      <Chip value={engine || "api"} options={engines.length ? engines : [{ value: engine || "api", label: engine || "api" }]} title="Engine — the next turn hands the conversation over" onChange={(v) => set({ engine: v }, `Engine ${v}`)} />
      <Chip
        value={model ?? ""}
        options={models}
        title={ownModel ? "Model for this session, from the next turn" : "This engine chooses its own model"}
        className={cn(!ownModel && "opacity-60")}
        onChange={(v) => set({ model: v }, models.find((m) => m.value === v)?.label ?? v)}
      />
      <Chip value={mode || "auto"} options={modes} title="What the agent may do without asking" onChange={(v) => set({ mode: v }, `Mode ${v}`)} />
    </>
  );
}
