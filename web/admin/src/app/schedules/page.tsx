"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { PlusIcon, PlayIcon, PauseIcon, Trash2Icon, HeartPulseIcon, ClockIcon, RepeatIcon, FolderIcon } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useGateway, useVersion } from "@/lib/store";
import type { ScheduleJob, ScheduleRun } from "@/lib/types";
import { baseName } from "@/lib/format";
import { Ago, Empty, ErrorNote, Field, Mono, NativeSelect, PageHeader } from "@/components/common";
import { FolderPicker } from "@/components/folder-picker";
import { PaneToggle, Workspace } from "@/components/workspace";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

export default function SchedulesPage() {
  return (
    <React.Suspense fallback={null}>
      <Schedules />
    </React.Suspense>
  );
}

const statusStyle: Record<string, string> = {
  ok: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  silent: "bg-muted text-muted-foreground",
  error: "bg-destructive/15 text-destructive",
  skipped: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  running: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
};

function StatusPill({ status }: { status?: string }) {
  if (!status) return null;
  return <span className={cn("rounded px-1.5 py-0.5 text-[11px] font-medium", statusStyle[status] ?? statusStyle.silent)}>{status}</span>;
}

/** until says how far away a time is, for "next run" — timeAgo only looks back. */
function until(iso?: string): string {
  if (!iso) return "";
  const s = Math.round((new Date(iso).getTime() - Date.now()) / 1000);
  if (s <= 30) return "due now";
  const m = Math.round(s / 60);
  if (m < 60) return `in ${m}m`;
  const h = Math.round(m / 60);
  if (h < 48) return `in ${h}h`;
  return `in ${Math.round(h / 24)}d`;
}

function Schedules() {
  const root = useGateway((s) => s.root);
  const params = useSearchParams();
  const router = useRouter();
  const v = useVersion("schedule");
  const { data, error } = useFetch<{ jobs: ScheduleJob[] }>("/api/schedules" + qs({ root }), [v, root]);
  const [draft, setDraft] = React.useState<ScheduleJob | null>(null);
  const [, tick] = React.useState(0);
  React.useEffect(() => {
    const t = setInterval(() => tick((n) => n + 1), 30000);
    return () => clearInterval(t);
  }, []);
  const jobs = data?.jobs ?? [];
  const want = params.get("id") ?? "";
  const selected = draft ?? jobs.find((j) => j.id === want) ?? null;

  const blank = (kind: ScheduleJob["kind"]): ScheduleJob => ({
    id: "",
    name: kind === "heartbeat" ? "heartbeat" : "",
    kind,
    root,
    prompt: "",
    every: kind === "heartbeat" ? "30m" : "1h",
    session: "thread",
    mode: "auto",
    enabled: true,
    state: {},
  });

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="Schedules"
        description={
          <>
            Work the agent does on its own, in the gateway — whether or not a terminal or this page is open. A <b>task</b> runs a prompt; a <b>heartbeat</b> works
            through the project&apos;s checklist in one turn and stays quiet when all is well. In a terminal, <Mono>/loop</Mono> repeats a prompt in that session.
          </>
        }
        actions={
          <>
            <Button variant="outline" size="sm" onClick={() => setDraft(blank("heartbeat"))}>
              <HeartPulseIcon /> Add heartbeat
            </Button>
            <Button size="sm" onClick={() => setDraft(blank("task"))}>
              <PlusIcon /> Add task
            </Button>
          </>
        }
      />
      <ErrorNote error={error} className="m-6" />
      <div className="min-h-0 flex-1">
        <Workspace selection={want || (draft ? "draft" : "")}
          id="schedules"
          left={{
            node: (
              <div className="h-full overflow-auto p-3">
                {jobs.length === 0 && <div className="px-1 py-2 text-sm text-muted-foreground">Nothing scheduled{root ? " in this project" : ""}.</div>}
                {jobs.map((j) => (
                  <button
                    key={j.id}
                    onClick={() => {
                      setDraft(null);
                      router.replace(`/schedules?id=${j.id}`);
                    }}
                    className={cn(
                      "mb-1 w-full rounded-lg border px-2.5 py-2 text-left hover:bg-muted/50",
                      selected?.id === j.id && "border-primary bg-muted",
                      !j.enabled && "opacity-60",
                    )}
                  >
                    <div className="flex items-center gap-1.5">
                      {j.kind === "heartbeat" ? (
                        <HeartPulseIcon className="size-3.5 shrink-0 text-rose-500" />
                      ) : j.session === "same" ? (
                        <RepeatIcon className="size-3.5 shrink-0 text-sky-500" />
                      ) : (
                        <ClockIcon className="size-3.5 shrink-0 text-muted-foreground" />
                      )}
                      <span className="truncate text-sm font-medium">{j.name}</span>
                      <span className="ml-auto">
                        <StatusPill status={j.state.running ? "running" : j.state.last_status} />
                      </span>
                    </div>
                    <div className="truncate text-[11px] text-muted-foreground">{j.describe}</div>
                    <div className="flex gap-2 text-[11px] text-muted-foreground">
                      <span className="truncate">{baseName(j.root)}</span>
                      <span className="ml-auto shrink-0">{!j.enabled ? "paused" : j.state.running ? "running now" : until(j.state.next)}</span>
                    </div>
                  </button>
                ))}
              </div>
            ),
            defaultSize: 320,
            minSize: 220,
            maxSize: 560,
            foldBelow: 820,
            label: "schedule list",
          }}
        >
          <div className="min-h-0 flex-1 overflow-auto p-6">
            <PaneToggle side="left" className="-mt-3 -ml-3 mb-1" />
            {selected ? (
              <JobEditor
                key={selected.id + (draft ? "d" + draft.kind : "")}
                job={selected}
                isNew={!!draft}
                onDone={(j) => {
                  setDraft(null);
                  router.replace(j ? `/schedules?id=${j.id}` : "/schedules");
                }}
              />
            ) : (
              <Empty title="Pick a schedule, or add one">
                A job&apos;s runs share one session you can open, each with a fresh context. Nothing to report means a quiet run: the agent answers HEARTBEAT_OK or NO_REPLY and nobody is notified. A failing
                job backs off (30s, 1m, 5m, 15m, 1h) and pauses itself after five failures in a row.
              </Empty>
            )}
          </div>
        </Workspace>
      </div>
    </div>
  );
}

type When = "every" | "cron" | "at" | "paced";

function whenOf(j: ScheduleJob): When {
  if (j.at && !j.at.startsWith("0001")) return "at";
  if (j.cron) return "cron";
  if (j.every) return "every";
  return "paced";
}

const isZero = (t?: string) => !t || t.startsWith("0001");

function localInput(iso?: string) {
  if (isZero(iso)) return "";
  const d = new Date(iso!);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

const dayNames = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

function JobEditor({ job, isNew, onDone }: { job: ScheduleJob; isNew: boolean; onDone: (j?: ScheduleJob) => void }) {
  const [name, setName] = React.useState(job.name);
  const [kind] = React.useState(job.kind);
  const [root, setRoot] = React.useState(job.root);
  const [picking, setPicking] = React.useState(false);
  const [prompt, setPrompt] = React.useState(job.prompt ?? "");
  const [when, setWhen] = React.useState<When>(whenOf(job));
  const [every, setEvery] = React.useState(job.every || "1h");
  const [cron, setCron] = React.useState(job.cron || "3 9 * * 1-5");
  const [at, setAt] = React.useState(localInput(job.at));
  const [paced, setPaced] = React.useState(!!job.pacing);
  const [pmin, setPmin] = React.useState(job.pacing?.min || "1m");
  const [pmax, setPmax] = React.useState(job.pacing?.max || "1h");
  const [hours, setHours] = React.useState(!!job.active_hours);
  const [hStart, setHStart] = React.useState(job.active_hours?.start || "09:00");
  const [hEnd, setHEnd] = React.useState(job.active_hours?.end || "18:00");
  const [days, setDays] = React.useState<number[]>(job.active_hours?.days ?? []);
  const [sessionStyle, setSessionStyle] = React.useState(job.session || "thread");
  const [mode, setMode] = React.useState(job.mode || "auto");
  const [model, setModel] = React.useState(job.model || "");
  const [gate, setGate] = React.useState(job.gate || "");
  const [timeout, setTimeoutValue] = React.useState(job.timeout || "");
  const [skipMissed, setSkipMissed] = React.useState(!!job.skip_missed);
  const [enabled, setEnabled] = React.useState(job.enabled);
  const [err, setErr] = React.useState<string | null>(null);

  const def = (): Partial<ScheduleJob> => ({
    name,
    kind,
    root,
    prompt: kind === "task" ? prompt : "",
    every: when === "every" ? every : "",
    cron: when === "cron" ? cron : "",
    at: when === "at" && at ? new Date(at).toISOString() : undefined,
    delete_after_run: when === "at" ? job.delete_after_run : false,
    pacing: paced || when === "paced" ? { min: pmin, max: pmax } : null,
    active_hours: hours ? { start: hStart, end: hEnd, days } : null,
    until: job.until,
    session: sessionStyle,
    session_id: job.session_id,
    engine: job.engine,
    model,
    mode,
    gate,
    timeout,
    skip_missed: skipMissed,
    enabled,
  });

  const save = async () => {
    setErr(null);
    try {
      const j = isNew ? await api.post<ScheduleJob>("/api/schedules", def()) : await api.put<ScheduleJob>(`/api/schedules/${job.id}`, def());
      toast.success(isNew ? "Scheduled" : "Saved");
      onDone(j);
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        {kind === "heartbeat" ? <HeartPulseIcon className="size-5 text-rose-500" /> : <ClockIcon className="size-5 text-muted-foreground" />}
        <h2 className="text-lg font-semibold">{isNew ? (kind === "heartbeat" ? "New heartbeat" : "New task") : job.name}</h2>
        {!isNew && <Badge variant="outline">{job.origin === "tui" ? "/loop" : job.origin || "web"}</Badge>}
        {!isNew && <StatusPill status={job.state.running ? "running" : job.state.last_status} />}
        {!isNew && (
          <div className="ml-auto flex gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={!!job.state.running}
              onClick={async () => {
                try {
                  await api.post(`/api/schedules/${job.id}/run`);
                  toast.success("Running now");
                } catch (e) {
                  toast.error((e as Error).message);
                }
              }}
            >
              <PlayIcon /> Run now
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={async () => {
                try {
                  await api.post(`/api/schedules/${job.id}/enable`, { enabled: !job.enabled });
                } catch (e) {
                  toast.error((e as Error).message);
                }
              }}
            >
              {job.enabled ? (
                <>
                  <PauseIcon /> Pause
                </>
              ) : (
                <>
                  <PlayIcon /> Resume
                </>
              )}
            </Button>
          </div>
        )}
      </div>

      {!isNew && (
        <div className="grid gap-x-6 gap-y-1 rounded-xl border bg-muted/30 px-4 py-3 text-sm sm:grid-cols-2">
          <div>
            <span className="text-muted-foreground">next </span>
            {!job.enabled ? "paused" : job.state.running ? "running now" : isZero(job.state.next) ? "—" : `${new Date(job.state.next!).toLocaleString()} (${until(job.state.next)})`}
          </div>
          <div>
            <span className="text-muted-foreground">last </span>
            {isZero(job.state.last_run) ? "never" : <Ago at={job.state.last_run} />} · {job.state.runs ?? 0} run{job.state.runs === 1 ? "" : "s"}
            {job.state.failures ? ` · ${job.state.failures} failing` : ""}
          </div>
          {job.state.running && (
            <div className="sm:col-span-2">
              <Link className="text-sky-600 hover:underline dark:text-sky-400" href={`/sessions?id=${job.state.running}`}>
                watch the run under way →
              </Link>
            </div>
          )}
          {job.state.last_error && <div className="text-destructive sm:col-span-2">{job.state.last_error}</div>}
        </div>
      )}

      <div className="grid gap-3 md:grid-cols-2">
        <Field label="Name">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={kind === "heartbeat" ? "heartbeat" : "morning PR review"} />
        </Field>
        <Field label="Project">
          <div className="flex gap-2">
            <Input value={root} onChange={(e) => setRoot(e.target.value)} className="font-mono text-xs" placeholder="C:\code\app" />
            <Button variant="outline" size="icon" onClick={() => setPicking(true)} title="Choose a folder">
              <FolderIcon />
            </Button>
          </div>
        </Field>
      </div>
      <FolderPicker open={picking} onOpenChange={setPicking} initial={root} onPick={setRoot} title="The project this runs in" />

      {kind === "task" ? (
        <Field label="Prompt" hint="What to do each run, as you would ask it. If there is nothing to report, the agent answers NO_REPLY and the run is quiet.">
          <Textarea rows={5} value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="Review the PRs opened since yesterday and list what needs me." />
        </Field>
      ) : (
        root && <Checklist key={root} root={root} />
      )}

      <div className="space-y-3 rounded-xl border p-4">
        <div className="text-sm font-medium">When</div>
        <div className="flex flex-wrap gap-2">
          {(
            [
              ["every", "Every"],
              ["cron", "Cron"],
              ["at", "Once"],
              ["paced", "Agent paces it"],
            ] as [When, string][]
          ).map(([w, label]) => (
            <Button key={w} size="sm" variant={when === w ? "default" : "outline"} onClick={() => setWhen(w)}>
              {label}
            </Button>
          ))}
        </div>
        {when === "every" && (
          <Field label="Interval" hint="30m, 2h, 1d — at least 1m. Counted from when the job was made, so a 6h job keeps its hours.">
            <Input value={every} onChange={(e) => setEvery(e.target.value)} className="w-40 font-mono" />
          </Field>
        )}
        {when === "cron" && (
          <Field label="Cron (local time)" hint="minute hour day month weekday. Jobs landing on :00 or :30 start up to 5 minutes late, the same each time, so they don't all start at once.">
            <Input value={cron} onChange={(e) => setCron(e.target.value)} className="w-56 font-mono" />
          </Field>
        )}
        {when === "at" && (
          <Field label="At">
            <Input type="datetime-local" value={at} onChange={(e) => setAt(e.target.value)} className="w-64" />
          </Field>
        )}
        {(when === "paced" || paced) && (
          <div className="flex flex-wrap items-end gap-3">
            <Field label="Agent picks, at least">
              <Input value={pmin} onChange={(e) => setPmin(e.target.value)} className="w-28 font-mono" />
            </Field>
            <Field label="at most">
              <Input value={pmax} onChange={(e) => setPmax(e.target.value)} className="w-28 font-mono" />
            </Field>
          </div>
        )}
        {when !== "paced" && when !== "at" && (
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={paced} onCheckedChange={setPaced} /> Let the agent choose each next run within bounds (it falls back to the upper bound)
          </label>
        )}
        <label className="flex items-center gap-2 text-sm">
          <Switch checked={hours} onCheckedChange={setHours} /> Only between certain hours
        </label>
        {hours && (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <Input type="time" value={hStart} onChange={(e) => setHStart(e.target.value)} className="w-32" />
            <span>to</span>
            <Input type="time" value={hEnd} onChange={(e) => setHEnd(e.target.value)} className="w-32" />
            <div className="flex gap-1">
              {dayNames.map((d, i) => (
                <button
                  key={d}
                  onClick={() => setDays(days.includes(i) ? days.filter((x) => x !== i) : [...days, i].sort())}
                  className={cn("rounded-md border px-1.5 py-0.5 text-xs", days.includes(i) ? "border-primary bg-primary text-primary-foreground" : "text-muted-foreground")}
                >
                  {d}
                </button>
              ))}
            </div>
            <span className="text-xs text-muted-foreground">{days.length ? "" : "every day"}</span>
          </div>
        )}
      </div>

      <div className="grid gap-3 md:grid-cols-3">
        <Field
          label="Session"
          hint={
            sessionStyle === "same"
              ? "One conversation, continued run after run: each run remembers the last."
              : sessionStyle === "new"
                ? "A session of its own each run."
                : "One session for the job, so runs do not crowd the list; each run starts with a fresh context."
          }
        >
          <NativeSelect
            value={sessionStyle}
            onChange={(v) => setSessionStyle(v as "thread" | "new" | "same")}
            options={[
              { value: "thread", label: "one for the job" },
              { value: "new", label: "new each run" },
              { value: "same", label: "one conversation" },
            ]}
          />
        </Field>
        <Field label="Mode" hint="A run that needs an approval waits for one here or in a terminal.">
          <NativeSelect
            value={mode}
            onChange={setMode}
            options={["plan", "ask", "auto", "full"].map((m) => ({ value: m, label: m }))}
          />
        </Field>
        <Field label="Model" hint="empty: the project's">
          <Input value={model} onChange={(e) => setModel(e.target.value)} className="font-mono text-xs" placeholder="default" />
        </Field>
      </div>

      <details className="rounded-xl border px-4 py-3" open={!!gate || !!timeout || skipMissed}>
        <summary className="cursor-pointer text-sm font-medium">More</summary>
        <div className="mt-3 space-y-3">
          <Field
            label="Gate"
            hint="A shell command run in the project before each run, with no model: the run goes ahead only if it exits 0, and what it prints is handed to the agent. Cheap polling."
          >
            <Input value={gate} onChange={(e) => setGate(e.target.value)} className="font-mono text-xs" placeholder="gh pr checks 123 | findstr fail" />
          </Field>
          <Field label="Timeout" hint="A run going on longer is stopped. Default 30m.">
            <Input value={timeout} onChange={(e) => setTimeoutValue(e.target.value)} className="w-32 font-mono" placeholder="30m" />
          </Field>
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={skipMissed} onCheckedChange={setSkipMissed} /> Skip runs missed while the gateway was off (otherwise: one catch-up run)
          </label>
        </div>
      </details>

      <label className="flex items-center gap-2 text-sm">
        <Switch checked={enabled} onCheckedChange={setEnabled} /> Enabled
      </label>
      <ErrorNote error={err} />
      <div className="flex gap-2">
        <Button onClick={save}>{isNew ? "Schedule" : "Save"}</Button>
        {isNew ? (
          <Button variant="ghost" onClick={() => onDone()}>
            Cancel
          </Button>
        ) : (
          <Button
            variant="destructive"
            onClick={async () => {
              if (!confirm(`Delete ${job.name} and its history?`)) return;
              try {
                await api.del(`/api/schedules/${job.id}`);
                onDone();
              } catch (e) {
                toast.error((e as Error).message);
              }
            }}
          >
            <Trash2Icon /> Delete
          </Button>
        )}
      </div>

      {!isNew && <Runs id={job.id} />}
    </div>
  );
}

function Checklist({ root }: { root: string }) {
  const { data } = useFetch<{ path: string; text: string }>("/api/schedules/checklist" + qs({ root }), [root]);
  const [text, setText] = React.useState<string | null>(null);
  const shown = text ?? data?.text ?? "";
  return (
    <Field
      label="Checklist"
      hint={
        <>
          <Mono>{data?.path ?? ".agent-tui/HEARTBEAT.md"}</Mono> — read fresh each run and worked through in one turn. Only headings and empty items? The run is
          skipped, at no cost. The agent can edit it too.
        </>
      }
    >
      <Textarea
        rows={8}
        value={shown}
        onChange={(e) => setText(e.target.value)}
        className="font-mono text-xs"
        placeholder={"# Heartbeat\n- If CI on main is red, find the failing job and say why.\n- If a PR of mine has new review comments, summarise them."}
      />
      {text !== null && text !== data?.text && (
        <Button
          size="sm"
          className="mt-2"
          onClick={async () => {
            try {
              await api.put("/api/schedules/checklist", { root, text });
              toast.success("Checklist saved");
            } catch (e) {
              toast.error((e as Error).message);
            }
          }}
        >
          Save checklist
        </Button>
      )}
    </Field>
  );
}

function Runs({ id }: { id: string }) {
  const v = useVersion("schedule");
  const { data } = useFetch<{ runs: ScheduleRun[] }>(`/api/schedules/${id}/runs`, [v]);
  const runs = data?.runs ?? [];
  return (
    <div className="space-y-2">
      <div className="text-sm font-medium">History</div>
      {runs.length === 0 && <div className="text-sm text-muted-foreground">No runs yet.</div>}
      {runs.map((r) => (
        <div key={r.id} className="rounded-lg border px-3 py-2 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            <StatusPill status={r.status} />
            <Ago at={r.start} />
            {r.reason && <span className="text-xs text-muted-foreground">{r.reason}</span>}
            {r.end && !isZero(r.end) && <span className="text-xs text-muted-foreground">{Math.max(1, Math.round((new Date(r.end).getTime() - new Date(r.start).getTime()) / 1000))}s</span>}
            {r.session && (
              <Link className="ml-auto text-xs text-sky-600 hover:underline dark:text-sky-400" href={`/sessions?id=${r.session}`}>
                open the session →
              </Link>
            )}
          </div>
          {(r.error || r.skipped) && <div className={cn("mt-1 text-xs", r.error ? "text-destructive" : "text-muted-foreground")}>{r.error || r.skipped}</div>}
          {r.text && r.status !== "silent" && <div className="mt-1 line-clamp-4 whitespace-pre-wrap text-xs text-muted-foreground">{r.text}</div>}
        </div>
      ))}
    </div>
  );
}
