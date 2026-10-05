"use client";

import * as React from "react";
import { EyeIcon } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useGateway, useVersion } from "@/lib/store";
import type { EngineInfo, Prefs, ProjectSettings, Trace } from "@/lib/types";
import { CopyButton, ErrorNote, Field, Mono, NativeSelect, PageHeader } from "@/components/common";
import { TraceView } from "@/components/trace-panel";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

interface SettingsData {
  prefs: Prefs;
  config: { model: string; effort: string; engine: string; mode: string; max_tokens: number; theme: string };
  models: { id: string; label: string; note?: string }[];
  modes: string[];
  paths: Record<string, string>;
  learn_default_model: string;
  project?: ProjectSettings;
  project_paths?: Record<string, string>;
  engines?: EngineInfo[];
}

const efforts = ["low", "medium", "high", "xhigh", "max"];

export default function SettingsPage() {
  const root = useGateway((s) => s.root);
  const v = useVersion("settings");
  const { data, error } = useFetch<SettingsData>("/api/settings" + qs({ root }), [v, root]);
  if (error) return <ErrorNote error={error} className="m-6" />;
  if (!data) return <div className="p-6 text-sm text-muted-foreground">Loading…</div>;
  return (
    <div>
      <PageHeader
        title="Settings"
        description="Changes save to the same files the terminal reads — a terminal picks them up on its next session or turn. A command-line flag still outranks both."
      />
      <div className="grid gap-6 p-6 xl:grid-cols-2">
        <GlobalCard key={JSON.stringify(data.prefs)} data={data} />
        {root ? <ProjectCard key={root + JSON.stringify(data.project)} data={data} root={root} /> : <Card><CardHeader><CardTitle>Project</CardTitle><CardDescription>Pick a project at the top to override settings for it.</CardDescription></CardHeader></Card>}
        <Effective data={data} root={root} />
        <ContextPreview root={root} engines={data.engines} />
        <Paths data={data} />
      </div>
    </div>
  );
}

function GlobalCard({ data }: { data: SettingsData }) {
  const [p, setP] = React.useState<Prefs>(data.prefs);
  const set = (patch: Partial<Prefs>) => setP({ ...p, ...patch });
  const save = async () => {
    try {
      await api.put("/api/settings/global", p);
      toast.success("Global settings saved");
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  const models = data.models.map((m) => ({ value: m.id, label: m.label }));
  return (
    <Card>
      <CardHeader>
        <CardTitle>Global</CardTitle>
        <CardDescription>
          <Mono>prefs.json</Mono> — the terminal&apos;s settings page writes the same file.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Engine" hint="empty: the one used last">
            <Input value={p.engine ?? ""} onChange={(e) => set({ engine: e.target.value })} placeholder="api | claude | codex | opencode" />
          </Field>
          <Field label="Model" hint={`empty: config.json (${data.config.model})`}>
            <NativeSelect value={p.model ?? ""} onChange={(v) => set({ model: v })} placeholder="from config" options={models} />
          </Field>
          <Field label="Mode" hint={`empty: config.json (${data.config.mode})`}>
            <NativeSelect value={p.mode ?? ""} onChange={(v) => set({ mode: v })} placeholder="from config" options={data.modes.map((m) => ({ value: m, label: m }))} />
          </Field>
          <Field label="Effort" hint={`empty: config.json (${data.config.effort})`}>
            <NativeSelect value={p.effort ?? ""} onChange={(v) => set({ effort: v })} placeholder="from config" options={efforts.map((m) => ({ value: m, label: m }))} />
          </Field>
        </div>
        <div className="grid grid-cols-2 gap-3 rounded-xl border p-3">
          <Field label="Automatic learning">
            <NativeSelect
              value={p.learn === false ? "off" : "on"}
              onChange={(v) => set({ learn: v === "on" ? null : false })}
              options={[
                { value: "on", label: "on" },
                { value: "off", label: "off" },
              ]}
            />
          </Field>
          <Field label="Learn skills from work">
            <NativeSelect
              value={p.learn_skills === false ? "off" : "on"}
              onChange={(v) => set({ learn_skills: v === "on" ? null : false })}
              options={[
                { value: "on", label: "on" },
                { value: "off", label: "off" },
              ]}
            />
          </Field>
          <Field label="Learning model" hint={`empty: ${data.learn_default_model}; runs after turns, in the background — a stronger model judges and merges better; Haiku is cheapest`} className="col-span-2">
            <NativeSelect value={p.learn_model ?? ""} onChange={(v) => set({ learn_model: v })} placeholder={`default (${data.models.find((m) => m.id === data.learn_default_model)?.label ?? data.learn_default_model})`} options={models} />
          </Field>
        </div>
        <Field label="Start the gateway with the terminal" hint="When a terminal finds no gateway, it starts `agent-tui serve` in the background so this page always has something to talk to.">
          <NativeSelect
            value={p.gateway_autostart === false ? "off" : "on"}
            onChange={(v) => set({ gateway_autostart: v === "on" ? null : false })}
            options={[
              { value: "on", label: "on" },
              { value: "off", label: "off" },
            ]}
          />
        </Field>
        <Field label="Instructions for every session" hint="Added to the system prompt in every project, before the project's own.">
          <Textarea rows={5} value={p.instructions ?? ""} onChange={(e) => set({ instructions: e.target.value })} />
        </Field>
        <div>
          <Button onClick={save}>Save global settings</Button>
        </div>
      </CardContent>
    </Card>
  );
}

function ProjectCard({ data, root }: { data: SettingsData; root: string }) {
  const [p, setP] = React.useState<ProjectSettings>(data.project ?? {});
  const set = (patch: Partial<ProjectSettings>) => setP({ ...p, ...patch });
  const save = async () => {
    try {
      await api.put("/api/settings/project", { root, settings: p });
      toast.success("Project settings saved");
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  return (
    <Card>
      <CardHeader>
        <CardTitle>Project</CardTitle>
        <CardDescription>
          <Mono>{data.project_paths?.settings}</Mono> — commit it to share with the team. Empty means “as global”.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Engine">
            <NativeSelect
              value={p.engine ?? ""}
              onChange={(v) => set({ engine: v })}
              placeholder="as global"
              options={(data.engines ?? []).map((e) => ({ value: e.id, label: e.label + (e.available ? "" : " (unavailable)") }))}
            />
          </Field>
          <Field label="Model">
            <NativeSelect value={p.model ?? ""} onChange={(v) => set({ model: v })} placeholder="as global" options={data.models.map((m) => ({ value: m.id, label: m.label }))} />
          </Field>
          <Field label="Mode">
            <NativeSelect value={p.mode ?? ""} onChange={(v) => set({ mode: v })} placeholder="as global" options={data.modes.map((m) => ({ value: m, label: m }))} />
          </Field>
          <Field label="Effort">
            <NativeSelect value={p.effort ?? ""} onChange={(v) => set({ effort: v })} placeholder="as global" options={efforts.map((m) => ({ value: m, label: m }))} />
          </Field>
          <Field label="Automatic learning" className="col-span-2">
            <NativeSelect
              value={p.learn === true ? "on" : p.learn === false ? "off" : ""}
              onChange={(v) => set({ learn: v === "" ? null : v === "on" })}
              placeholder="as global"
              options={[
                { value: "on", label: "on in this project" },
                { value: "off", label: "off in this project" },
              ]}
            />
          </Field>
        </div>
        <Field label="Project instructions" hint="Standing orders for the agent in this project (AGENTS.md in the repository is read as well).">
          <Textarea rows={5} value={p.instructions ?? ""} onChange={(e) => set({ instructions: e.target.value })} />
        </Field>
        <div className="text-xs text-muted-foreground">
          Switched off here: skills {p.disabled_skills?.length ? p.disabled_skills.join(", ") : "none"} · MCP {p.disabled_mcp?.length ? p.disabled_mcp.join(", ") : "none"}{" "}
          (toggle them on the Skills and MCP pages).
        </div>
        <div>
          <Button onClick={save}>Save project settings</Button>
        </div>
      </CardContent>
    </Card>
  );
}

function Effective({ data, root }: { data: SettingsData; root: string }) {
  const pick = (proj?: string, pref?: string, cfg?: string): [string, string] => {
    if (root && proj) return [proj, "project"];
    if (pref) return [pref, "global"];
    return [cfg || "—", "config.json"];
  };
  const rows: [string, [string, string]][] = [
    ["engine", pick(data.project?.engine, data.prefs.engine, data.config.engine || "last used")],
    ["model", pick(data.project?.model, data.prefs.model, data.config.model)],
    ["mode", pick(data.project?.mode, data.prefs.mode, data.config.mode)],
    ["effort", pick(data.project?.effort, data.prefs.effort, data.config.effort)],
    [
      "learning",
      root && data.project?.learn != null ? [data.project.learn ? "on" : "off", "project"] : [data.prefs.learn === false ? "off" : "on", "global"],
    ],
  ];
  return (
    <Card>
      <CardHeader>
        <CardTitle>What a new session gets</CardTitle>
        <CardDescription>Resolved as flag › project › global › config.json, and where each value came from.</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="divide-y rounded-xl border">
          {rows.map(([k, [v, src]]) => (
            <div key={k} className="flex items-center gap-3 px-3 py-2 text-sm">
              <span className="w-20 text-muted-foreground">{k}</span>
              <span className="flex-1 font-mono">{v}</span>
              <Badge variant={src === "project" ? "default" : src === "global" ? "secondary" : "outline"} className="font-normal">
                {src}
              </Badge>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

function ContextPreview({ root, engines }: { root: string; engines?: EngineInfo[] }) {
  const [prompt, setPrompt] = React.useState("");
  const [engine, setEngine] = React.useState("api");
  const [trace, setTrace] = React.useState<Trace | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState<string | null>(null);
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <EyeIcon className="size-4" /> Context preview
        </CardTitle>
        <CardDescription>What would the agent be given for this prompt, here, now? Nothing runs and nothing is counted as recalled.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <Textarea rows={3} value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="e.g. how do we deploy this?" />
        <div className="flex gap-2">
          <NativeSelect value={engine} onChange={setEngine} options={(engines ?? [{ id: "api", label: "api" } as EngineInfo]).map((e) => ({ value: e.id, label: e.label }))} />
          <Button
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setErr(null);
              try {
                setTrace(await api.post<Trace>("/api/context/preview", { root, engine, prompt }));
              } catch (e) {
                setErr((e as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? "Assembling…" : "Preview"}
          </Button>
        </div>
        <ErrorNote error={err} />
        {trace && <TraceView t={trace} />}
      </CardContent>
    </Card>
  );
}

function Paths({ data }: { data: SettingsData }) {
  const all = { ...data.paths, ...Object.fromEntries(Object.entries(data.project_paths ?? {}).map(([k, v]) => ["project " + k, v])) };
  return (
    <Card className="xl:col-span-2">
      <CardHeader>
        <CardTitle>Where things live</CardTitle>
      </CardHeader>
      <CardContent className="grid gap-1.5 text-sm">
        {Object.entries(all).map(([k, v]) => (
          <div key={k} className="flex items-center gap-3">
            <span className="w-36 shrink-0 text-muted-foreground">{k.replace(/_/g, " ")}</span>
            <Mono className="truncate">{v}</Mono>
            <CopyButton text={v} />
          </div>
        ))}
      </CardContent>
    </Card>
  );
}
