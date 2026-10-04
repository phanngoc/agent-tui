"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { PlusIcon, SearchIcon, SquareIcon, SendIcon, GraduationCapIcon, RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { onEvent, useGateway, useVersion } from "@/lib/store";
import type { Message, MemoryRecord, Session, SessionState, Summary, Trace, Live, EngineInfo } from "@/lib/types";
import { Ago, CopyButton, Dot, Empty, ErrorNote, Field, Mono, NativeSelect, Pre } from "@/components/common";
import { OwnerBadge } from "@/components/owner-badge";
import { LiveTail, MessageView } from "@/components/transcript";
import { TracePanel } from "@/components/trace-panel";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { baseName, memoryTypeLabel, tokens } from "@/lib/format";

export default function SessionsPage() {
  return (
    <React.Suspense fallback={<div className="p-6 text-sm text-muted-foreground">Loading…</div>}>
      <Sessions />
    </React.Suspense>
  );
}

function Sessions() {
  const params = useSearchParams();
  const router = useRouter();
  const id = params.get("id") ?? "";
  const [creating, setCreating] = React.useState(params.get("new") === "1");
  const root = useGateway((s) => s.root);
  return (
    <div className="flex h-full min-h-0">
      <SessionList selected={id} onSelect={(s) => router.push(`/sessions?id=${s}`)} onNew={() => setCreating(true)} />
      <div className="flex min-w-0 flex-1 flex-col">
        {id ? (
          <Conversation key={id} id={id} />
        ) : (
          <div className="flex flex-1 items-center justify-center p-6">
            <Empty title="Pick a conversation, or start one">
              Every session saved on this machine is listed — the terminal&apos;s and the web&apos;s alike. A prompt sent here to a session open in a terminal runs
              there, and shows here live.
              <div className="mt-3">
                <Button size="sm" onClick={() => setCreating(true)}>
                  <PlusIcon /> New conversation
                </Button>
              </div>
            </Empty>
          </div>
        )}
      </div>
      <NewSessionDialog key={root} open={creating} onOpenChange={setCreating} onCreated={(sid) => router.push(`/sessions?id=${sid}`)} />
    </div>
  );
}

function SessionList({ selected, onSelect, onNew }: { selected: string; onSelect: (id: string) => void; onNew: () => void }) {
  const root = useGateway((s) => s.root);
  const live = useGateway((s) => s.live);
  const [all, setAll] = React.useState(false);
  const [q, setQ] = React.useState("");
  const v = useVersion("sessions");
  const { data, error } = useFetch<Summary[]>("/api/sessions" + qs({ root: all ? "" : root, q }), [v]);

  return (
    <div className="flex w-80 shrink-0 flex-col border-r">
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
        <div className="flex gap-1 text-xs">
          {root && (
            <button className={cn("rounded px-2 py-0.5", !all ? "bg-muted font-medium" : "text-muted-foreground")} onClick={() => setAll(false)}>
              {baseName(root)}
            </button>
          )}
          <button className={cn("rounded px-2 py-0.5", all || !root ? "bg-muted font-medium" : "text-muted-foreground")} onClick={() => setAll(true)}>
            All projects
          </button>
          <span className="ml-auto text-muted-foreground">{data?.length ?? 0}</span>
        </div>
      </div>
      <ErrorNote error={error} className="m-3" />
      <div className="min-h-0 flex-1 overflow-auto">
        {(data ?? []).map((s) => {
          const l = live[s.id];
          const busy = l?.busy ?? s.busy;
          const waiting = l && Object.keys(l.approvals).length + Object.keys(l.choices).length > 0;
          return (
            <button
              key={s.id}
              onClick={() => onSelect(s.id)}
              className={cn("flex w-full flex-col gap-1 border-b px-3 py-2.5 text-left hover:bg-muted/50", selected === s.id && "bg-muted")}
            >
              <div className="flex items-center gap-2">
                <Dot on={busy} pulse />
                <span className="min-w-0 flex-1 truncate text-sm font-medium">{s.title || "(untitled)"}</span>
                {waiting && <span className="rounded bg-amber-500/15 px-1 text-[10px] font-medium text-amber-700 dark:text-amber-300">needs you</span>}
              </div>
              <div className="flex items-center gap-2 pl-4 text-xs text-muted-foreground">
                <span className="truncate">{baseName(s.root)}</span>
                <span>·</span>
                <span>{s.engine || "api"}</span>
                <span>·</span>
                <span>{s.messages} msg</span>
                <span className="ml-auto">
                  <Ago at={s.updated} />
                </span>
              </div>
              {s.owner?.startsWith("tui") && <div className="pl-4 text-[11px] text-primary">open in a terminal</div>}
            </button>
          );
        })}
      </div>
    </div>
  );
}

interface Detail {
  summary: Summary;
  session: Session;
  live: Live;
  traces: Trace[];
  learning?: SessionState;
}

function Conversation({ id }: { id: string }) {
  const { data, error, reload, setData } = useFetch<Detail>(`/api/sessions/${id}`, []);
  const live = useGateway((s) => s.live[id]);
  const summary = useGateway((s) => s.summaries[id]);
  const [traceFor, setTraceFor] = React.useState<number | null>(null);
  const [tab, setTab] = React.useState("context");
  const bottom = React.useRef<HTMLDivElement>(null);

  // Messages arrive as events; a turn's end refetches to settle on disk.
  React.useEffect(
    () =>
      onEvent((e) => {
        if (e.session !== id) return;
        if (e.type === "message") {
          setData((d) => {
            if (!d) return d;
            const msgs: Message[] = [...d.session.messages];
            msgs[e.data.index] = e.data.message;
            return { ...d, session: { ...d.session, messages: msgs } };
          });
        }
        if (e.type === "turn.done" || e.type === "turn.started") setTimeout(() => void reload(), 400);
      }),
    [id, reload, setData],
  );

  const count = data?.session.messages.length ?? 0;
  React.useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" });
  }, [count, live?.partial, live?.busy]);

  const owner = summary?.owner ?? data?.summary.owner;
  const busy = live?.busy ?? false;

  const send = async (text: string) => {
    try {
      const r = await api.post<{ owner: string }>(`/api/sessions/${id}/prompt`, { text });
      toast.success(r.owner.startsWith("tui") ? "Sent to the terminal holding this session" : "Running in the gateway");
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  const cmd = async (path: string, body: unknown) => {
    try {
      await api.post(`/api/sessions/${id}/${path}`, body);
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  if (error) return <ErrorNote error={error} className="m-6" />;
  if (!data) return <div className="p-6 text-sm text-muted-foreground">Loading…</div>;
  const s = data.session;

  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex items-start gap-3 border-b px-5 py-3">
          <div className="min-w-0 flex-1">
            <div className="truncate font-semibold">{s.title || "(untitled)"}</div>
            <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
              <span className="font-mono">{s.root}</span>
              <Badge variant="outline" className="font-normal">
                {s.engine || "api"}
              </Badge>
              <Badge variant="outline" className="font-normal">
                {s.model}
              </Badge>
              <Badge variant="outline" className="font-normal">
                mode {s.mode || "auto"}
              </Badge>
              {s.target && s.target !== "host" && (
                <Badge variant="outline" className="font-normal">
                  {s.target}
                </Badge>
              )}
              <OwnerBadge owner={owner} />
              <span>
                {tokens(s.input_tokens)} in · {tokens(s.output_tokens)} out
              </span>
            </div>
          </div>
          <div className="flex shrink-0 gap-2">
            {busy && (
              <Button size="sm" variant="destructive" onClick={() => cmd("cancel", {})}>
                <SquareIcon /> Stop
              </Button>
            )}
            <LearnNowButton id={id} />
            <Button size="icon-sm" variant="ghost" title="Reload from disk" onClick={() => reload()}>
              <RefreshCwIcon />
            </Button>
          </div>
        </div>

        <div className="min-h-0 flex-1 overflow-auto px-5 py-4">
          <div className="mx-auto max-w-3xl space-y-5">
            {s.messages.map((m, i) => (
              <MessageView
                key={i}
                m={m}
                index={i}
                onTrace={(idx) => {
                  setTraceFor(idx);
                  setTab("context");
                }}
              />
            ))}
            <LiveTail live={live} onApprove={(aid, verdict) => cmd("approve", { id: aid, verdict })} onChoose={(cid, index) => cmd("choose", { id: cid, index })} />
            <div ref={bottom} />
          </div>
        </div>

        <Composer busy={busy} owner={owner} target={s.target} onSend={send} />
      </div>

      <aside className="flex w-[26rem] shrink-0 flex-col border-l">
        <Tabs value={tab} onValueChange={(v) => setTab(String(v))} className="flex min-h-0 flex-1 flex-col gap-0">
          <div className="border-b px-3 py-2">
            <TabsList>
              <TabsTrigger value="context">Context trace</TabsTrigger>
              <TabsTrigger value="learned">Learned</TabsTrigger>
              <TabsTrigger value="details">Details</TabsTrigger>
            </TabsList>
          </div>
          <TabsContent value="context" className="min-h-0 flex-1 overflow-auto p-3">
            <TracePanel traces={data.traces} messages={s.messages} focus={traceFor} />
          </TabsContent>
          <TabsContent value="learned" className="min-h-0 flex-1 overflow-auto p-3">
            <LearnedPanel id={id} root={s.root} state={data.learning} count={s.messages.length} />
          </TabsContent>
          <TabsContent value="details" className="min-h-0 flex-1 overflow-auto p-3">
            <DetailsPanel s={s} />
          </TabsContent>
        </Tabs>
      </aside>
    </div>
  );
}

function Composer({ busy, owner, target, onSend }: { busy: boolean; owner?: string; target?: string; onSend: (t: string) => Promise<void> }) {
  const [text, setText] = React.useState("");
  const [sending, setSending] = React.useState(false);
  const go = async () => {
    const t = text.trim();
    if (!t) return;
    setSending(true);
    await onSend(t);
    setSending(false);
    setText("");
  };
  return (
    <div className="border-t p-3">
      <div className="mx-auto max-w-3xl">
        <div className="flex items-end gap-2 rounded-xl border bg-background p-2 focus-within:ring-[3px] focus-within:ring-ring/30">
          <Textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
                e.preventDefault();
                void go();
              }
            }}
            placeholder={busy ? "A turn is running — this will queue (in a terminal) or wait" : "Ask anything…  (Ctrl+Enter to send)"}
            className="max-h-48 min-h-10 resize-none border-0 shadow-none focus-visible:ring-0"
          />
          <Button onClick={go} disabled={sending || !text.trim()} size="icon">
            <SendIcon />
          </Button>
        </div>
        <div className="mt-1.5 text-[11px] text-muted-foreground">
          {owner?.startsWith("tui")
            ? "This session is open in a terminal: your prompt runs there, exactly as if typed, and streams here."
            : target && target !== "host"
              ? `This session works inside ${target}; only a terminal can reach it. Open it in tui and prompts sent here will run there.`
              : "No terminal holds this session: the gateway runs it, with the same engines, memory, skills and MCP servers."}
        </div>
      </div>
    </div>
  );
}

function LearnNowButton({ id }: { id: string }) {
  const [busy, setBusy] = React.useState(false);
  return (
    <Button
      size="sm"
      variant="outline"
      disabled={busy}
      title="Run memory extraction on this session now, whatever the turn counters say"
      onClick={async () => {
        setBusy(true);
        try {
          await api.post(`/api/sessions/${id}/learn`);
          toast.success("Learned from this session — see the Learned tab");
        } catch (e) {
          toast.error((e as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <GraduationCapIcon /> {busy ? "Learning…" : "Learn now"}
    </Button>
  );
}

function LearnedPanel({ id, root, state, count }: { id: string; root: string; state?: SessionState; count: number }) {
  const v = useVersion("memory", "learn");
  const { data } = useFetch<{ hits: { record: MemoryRecord }[] }>("/api/memory" + qs({ root, session: id }), [v]);
  const recs = (data?.hits ?? []).map((h) => h.record);
  return (
    <div className="space-y-4">
      <div className="rounded-lg border p-3 text-xs">
        <div className="mb-1 font-medium">Learner bookmark</div>
        {state ? (
          <div className="grid grid-cols-2 gap-1 text-muted-foreground">
            <span>read up to message</span>
            <span className="text-foreground">
              {state.cursor} of {count}
            </span>
            <span>turns since extraction</span>
            <span className="text-foreground">
              {state.turns} (next at {state.threshold})
            </span>
            <span>skill review read to</span>
            <span className="text-foreground">{state.skill_cursor}</span>
            <span>current scene</span>
            <span className="text-foreground">{state.scene || "—"}</span>
          </div>
        ) : (
          <div className="text-muted-foreground">Not learned from yet. It happens after turns, after ten idle minutes, or with “Learn now”.</div>
        )}
      </div>
      <div className="text-xs font-medium">Memories that came from this session ({recs.length})</div>
      {recs.length === 0 && <Empty title="None yet" />}
      {recs.map((r) => (
        <Link key={r.id} href={`/memory?ids=${r.id}`} className="block rounded-lg border p-2.5 text-sm hover:bg-muted/40">
          <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
            <Badge variant="secondary" className="font-normal">
              {memoryTypeLabel[r.type] ?? r.type}
            </Badge>
            <span>{r.scope}</span>
            <span>p{r.priority}</span>
            {r.sources?.length ? <span>from #{r.sources.join(", #")}</span> : null}
            <span className="ml-auto">v{r.version}</span>
          </div>
          {r.content}
        </Link>
      ))}
    </div>
  );
}

function DetailsPanel({ s }: { s: Session }) {
  const rows: [string, React.ReactNode][] = [
    ["id", <span key="id" className="flex items-center gap-1"><Mono>{s.id}</Mono><CopyButton text={s.id} /></span>],
    ["root", <Mono key="r">{s.root}</Mono>],
    ["cwd", <Mono key="c">{s.cwd || s.root}</Mono>],
    ["created", <Ago key="cr" at={s.created} />],
    ["updated", <Ago key="up" at={s.updated} />],
    ["tokens", `${tokens(s.input_tokens)} in · ${tokens(s.output_tokens)} out · ${tokens(s.cache_reads)} cache reads`],
  ];
  return (
    <div className="space-y-4 text-sm">
      <div className="grid grid-cols-[6rem_1fr] gap-x-2 gap-y-1.5">
        {rows.map(([k, v]) => (
          <React.Fragment key={k}>
            <span className="text-muted-foreground">{k}</span>
            <span className="min-w-0 break-all">{v}</span>
          </React.Fragment>
        ))}
      </div>
      <div>
        <div className="mb-1 text-xs font-medium">Engine state</div>
        <Pre>{JSON.stringify(s.engines ?? {}, null, 2)}</Pre>
        <p className="mt-1 text-xs text-muted-foreground">
          Each engine keeps its own session id (to resume) and how many messages it has seen; the rest is handed over as a brief when engines change.
        </p>
      </div>
    </div>
  );
}

function NewSessionDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (o: boolean) => void; onCreated: (id: string) => void }) {
  const root = useGateway((s) => s.root);
  const [dir, setDir] = React.useState(root);
  const [engine, setEngine] = React.useState("");
  const [model, setModel] = React.useState("");
  const [mode, setMode] = React.useState("");
  const [prompt, setPrompt] = React.useState("");
  const [err, setErr] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);
  const { data: eng } = useFetch<{ engines: EngineInfo[]; models: { id: string; label: string }[]; modes: string[] }>(
    open && dir ? "/api/engines" + qs({ root: dir }) : null,
    [dir, open],
  );

  const create = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await api.post<{ id: string }>("/api/sessions", { root: dir, engine, model, mode, prompt });
      onOpenChange(false);
      setPrompt("");
      onCreated(r.id);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => onOpenChange(o)}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>New conversation</DialogTitle>
          <DialogDescription>Runs in the gateway with the project&apos;s memory, skills and MCP servers. Empty choices follow the project, then global, settings.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3">
          <Field label="Project folder">
            <Input value={dir} onChange={(e) => setDir(e.target.value)} className="font-mono text-xs" placeholder="C:\\code\\project" />
          </Field>
          <div className="grid grid-cols-3 gap-2">
            <Field label="Engine">
              <NativeSelect
                value={engine}
                onChange={setEngine}
                placeholder="settings"
                options={(eng?.engines ?? []).map((e) => ({ value: e.id, label: e.label + (e.available ? "" : " (unavailable)"), disabled: !e.available }))}
              />
            </Field>
            <Field label="Model">
              <NativeSelect value={model} onChange={setModel} placeholder="settings" options={(eng?.models ?? []).map((m) => ({ value: m.id, label: m.label }))} />
            </Field>
            <Field label="Mode">
              <NativeSelect value={mode} onChange={setMode} placeholder="settings" options={(eng?.modes ?? []).map((m) => ({ value: m, label: m }))} />
            </Field>
          </div>
          <Field label="First prompt">
            <Textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={5} placeholder="What should the agent do?" />
          </Field>
          <ErrorNote error={err} />
        </div>
        <DialogFooter>
          <Button disabled={busy || !dir || !prompt.trim()} onClick={create}>
            {busy ? "Starting…" : "Start"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
