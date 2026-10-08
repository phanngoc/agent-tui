"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { FastForwardIcon, SquareIcon, SendIcon, GraduationCapIcon, RefreshCwIcon, MousePointerClickIcon, ArrowDownIcon, SquareTerminalIcon, CodeXmlIcon } from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useFollow } from "@/lib/follow";
import { onEvent, useGateway, useVersion } from "@/lib/store";
import type { Message, MemoryRecord, QueueData, Session, SessionState, Summary, Trace, Live } from "@/lib/types";
import { Ago, CopyButton, Empty, ErrorNote, Mono, Pre } from "@/components/common";
import { OwnerBadge } from "@/components/owner-badge";
import { CredentialBadge } from "@/components/token-pool";
import { AssistantRun, LiveTail, MessageView, ProjectRoot, groupTurns } from "@/components/transcript";
import { TracePanel } from "@/components/trace-panel";
import { PaneToggle, RightPane, Workspace, useWorkspace } from "@/components/workspace";
import { FocusButton } from "@/components/focus-button";
import { SessionSettings } from "@/components/session-settings";
import { Explorer } from "@/components/explorer";
import { TerminalPanel } from "@/components/terminal-panel";
import { FileOpener } from "@/lib/file-opener";
import { useIsMobile } from "@/lib/mobile";
import { SelectionAction, withQuotes } from "@/components/selection-action";
import { BranchPicker } from "@/components/branch-picker";
import { AttachButton, AttachmentStrip, DropHint, filesOf, useAttachments, type Attached } from "@/components/attachments";
import { describePath } from "@/components/folder-picker";
import { NewChat, sendOnEnter } from "@/components/new-chat";
import { RenameInput, SessionList, setListSettings } from "@/components/session-list";
import { Chapters, MessageHooksContext, type MessageHooks } from "@/components/message-actions";
import { useChatDraft } from "@/lib/draft";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
  const root = useGateway((s) => s.root);
  // Each "new" is a fresh draft, even when one is already on screen.
  const [fresh, setFresh] = React.useState(0);
  const list = (
    <SessionList
      selected={id}
      onSelect={(s) => router.push(`/sessions?id=${s}`)}
      onNew={() => {
        setFresh((n) => n + 1);
        router.push("/sessions");
      }}
    />
  );
  return (
    <Workspace selection={id || (fresh ? `new-${fresh}` : "")}
      id="sessions"
      left={{ node: list, defaultSize: 300, minSize: 220, maxSize: 520, foldBelow: 960, label: "session list" }}
      right={{
        node: id ? null : <SidePlaceholder />,
        defaultSize: 400,
        minSize: 300,
        maxSize: 720,
        foldBelow: 1400,
        label: "side panel",
      }}
    >
      {id ? (
        <Conversation key={id} id={id} />
      ) : (
        <>
          <div className="flex items-center gap-1 px-3 pt-2">
            <PaneToggle side="left" />
            <div className="ml-auto flex items-center gap-1">
              <FocusButton />
              <PaneToggle side="right" />
            </div>
          </div>
          <NewChat key={root + fresh} root={root} onCreated={(sid) => router.push(`/sessions?id=${sid}`)} />
        </>
      )}
    </Workspace>
  );
}

/** Title is the conversation's name in its header: click it to rename. */
function Title({ id, title }: { id: string; title: string }) {
  const [editing, setEditing] = React.useState(false);
  // A name just given shows until the gateway's own word on it arrives.
  const [given, setGiven] = React.useState<{ value: string; over: string } | null>(null);
  const shown = given && given.over === title ? given.value : title;
  if (editing)
    return (
      <div className="flex">
        <RenameInput
          initial={shown}
          className="font-semibold"
          onDone={(t) => {
            setEditing(false);
            if (t === null) return;
            t = t.trim().replace(/\s+/g, " ");
            if (t === shown) return;
            setGiven({ value: t || "new session", over: title });
            setListSettings(id, { title: t }).catch((e: Error) => {
              setGiven(null);
              toast.error(e.message);
            });
          }}
        />
      </div>
    );
  return (
    <button onClick={() => setEditing(true)} title="Rename" className="block max-w-full truncate rounded text-left font-semibold hover:bg-muted/60">
      {shown || "(untitled)"}
    </button>
  );
}

interface Detail {
  summary: Summary;
  session: Session;
  live: Live;
  traces: Trace[];
  learning?: SessionState;
  queue?: QueueData;
}

function Conversation({ id }: { id: string }) {
  const { data, error, reload, setData } = useFetch<Detail>(`/api/sessions/${id}`, []);
  const live = useGateway((s) => s.live[id]);
  const summary = useGateway((s) => s.summaries[id]);
  const [traceFor, setTraceFor] = React.useState<number | null>(null);
  const [tab, setTab] = React.useState("context");
  const ws = useWorkspace();
  // A path clicked in the conversation opens in the Files tab.
  const [fileReq, setFileReq] = React.useState<{ path: string; seq: number } | undefined>();
  const openFile = React.useCallback(
    (path: string) => {
      setFileReq({ path, seq: Date.now() });
      setTab("files");
      ws.openRight();
    },
    [ws],
  );
  // The terminal panel, open or not, remembered across reloads.
  const [termOpen, setTermOpen] = React.useState(() => {
    try {
      return localStorage.getItem("agent-tui.term") === "1";
    } catch {
      return false;
    }
  });
  const toggleTerm = React.useCallback((on?: boolean) => {
    setTermOpen((cur) => {
      const next = on ?? !cur;
      try {
        localStorage.setItem("agent-tui.term", next ? "1" : "0");
      } catch {}
      return next;
    });
  }, []);
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey && e.key === "`") {
        e.preventDefault();
        toggleTerm();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [toggleTerm]);
  const { scroller, content, below, toBottom } = useFollow(!!data);
  // Selections added to the next message, quoted above the composer.
  const [quotes, setQuotes] = React.useState<string[]>([]);
  // Messages sent while a turn runs, waiting for the agent (Claude Code's
  // queue): from the session as loaded, then from its events.
  const [queue, setQueue] = React.useState<QueueData>({ items: [], steers: false });
  const loadedQueue = data?.queue;
  const [seenQueue, setSeenQueue] = React.useState<QueueData | undefined>();
  if (loadedQueue && loadedQueue !== seenQueue) {
    setSeenQueue(loadedQueue);
    setQueue(loadedQueue);
  }
  React.useEffect(
    () =>
      onEvent((e) => {
        if (e.session === id && e.type === "queue" && e.data) setQueue(e.data as QueueData);
      }),
    [id],
  );
  // What was just chosen in the header, shown until the session says so.
  const [chosen, setChosen] = React.useState<{ engine?: string; model?: string; mode?: string }>({});

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
        if (e.type === "subagent") {
          // A sub-agent's progress belongs on the Agent call that started it.
          setData((d) => {
            if (!d) return d;
            const { tool_use, agent } = e.data;
            const msgs = d.session.messages.map((m) =>
              m.tools?.some((t) => t.id === tool_use) ? { ...m, tools: m.tools.map((t) => (t.id === tool_use ? { ...t, agent } : t)) } : m,
            );
            return { ...d, session: { ...d.session, messages: msgs } };
          });
        }
        if (e.type === "tool.done") {
          // A finished call's result belongs on the message that made it.
          setData((d) => {
            if (!d) return d;
            const call = e.data.call;
            const msgs = d.session.messages.map((m) =>
              m.tools?.some((t) => t.id === call.id)
                ? { ...m, tools: m.tools.map((t) => (t.id === call.id ? { ...t, ...call, name: call.name || t.name, input: call.input ?? t.input } : t)) }
                : m,
            );
            return { ...d, session: { ...d.session, messages: msgs } };
          });
        }
        if (e.type === "turn.done" || e.type === "turn.started") setTimeout(() => void reload(), 400);
      }),
    [id, reload, setData],
  );

  const owner = summary?.owner ?? data?.summary.owner;
  const busy = live?.busy ?? false;
  const busyRef = React.useRef(busy);
  React.useEffect(() => {
    busyRef.current = busy;
  }, [busy]);

  const send = async (text: string, now = false, files: Attached[] = []) => {
    toBottom();
    try {
      const r = await api.post<{ owner: string }>(`/api/sessions/${id}/prompt`, { text, now, files: files.length ? filesOf(files) : undefined });
      if (now) toast("Stopping the turn to send it now");
      else if (busyRef.current) toast(queue.steers ? "Queued: the agent gets it after its current step" : "Queued: it goes when this turn ends");
      else toast.success(r.owner.startsWith("tui") ? "Sent to the terminal holding this session" : "Running in the gateway");
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  const unqueue = async (item: string): Promise<string | null> => {
    try {
      const r = await api.post<{ text: string }>(`/api/sessions/${id}/unqueue`, { item });
      return r.text;
    } catch (e) {
      toast.error((e as Error).message);
      return null;
    }
  };
  const cmd = async (path: string, body: unknown) => {
    try {
      await api.post(`/api/sessions/${id}/${path}`, body);
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  // A chapter just pinned or unpinned shows until the session says so.
  const [marks, setMarks] = React.useState<Record<number, boolean>>({});
  const router = useRouter();
  const messages = data?.session.messages;
  const chapters = React.useMemo(() => {
    const set = new Set(data?.session.chapters ?? []);
    for (const [i, on] of Object.entries(marks)) {
      if (on) set.add(Number(i));
      else set.delete(Number(i));
    }
    return [...set].sort((a, b) => a - b);
  }, [data?.session.chapters, marks]);
  const hooks = React.useMemo<MessageHooks | null>(() => {
    if (!messages) return null;
    return {
      session: id,
      messages,
      chapters,
      chapter: (at, on) => {
        setMarks((m) => ({ ...m, [at]: on }));
        setListSettings(id, { chapter: { at, on } }).catch((e: Error) => {
          setMarks((m) => ({ ...m, [at]: !on }));
          toast.error(e.message);
        });
      },
      fork: async (at) => {
        // From a prompt: up to the message before it, with the prompt in
        // the new conversation's composer to change and send again.
        const m = messages[at];
        const prompt = m?.role === "user" ? (m.text ?? "") : "";
        const upTo = m?.role === "user" ? at - 1 : at;
        try {
          if (upTo < 0) {
            useGateway.getState().setRoot(data!.session.root);
            if (prompt) useChatDraft.getState().push(prompt);
            router.push("/sessions");
            return;
          }
          const r = await api.post<{ id: string }>(`/api/sessions/${id}/fork`, { at: upTo });
          if (prompt) useChatDraft.getState().push(prompt);
          toast.success("Forked from here", { description: prompt ? "Edit the prompt and send it." : "The agent is briefed on the conversation so far." });
          router.push(`/sessions?id=${r.id}`);
        } catch (e) {
          toast.error((e as Error).message);
        }
      },
    };
  }, [id, messages, chapters, router, data]);
  const turns = React.useMemo(() => groupTurns(messages ?? []), [messages]);
  const approve = (aid: string, verdict: "allow" | "allow_all" | "deny") => cmd("approve", { id: aid, verdict });
  const choose = (cid: string, index: number) => cmd("choose", { id: cid, index });

  if (error) return <ErrorNote error={error} className="m-6" />;
  if (!data) return <div className="p-6 text-sm text-muted-foreground">Loading…</div>;
  const s = data.session;

  return (
    <FileOpener.Provider value={openFile}>
      <ProjectRoot.Provider value={s.root}>
    <MessageHooksContext.Provider value={hooks}>
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="@container flex items-start gap-2 border-b px-3 py-2 md:gap-3 md:px-5 md:py-3">
          <PaneToggle side="left" className="-ml-2" />
          <div className="min-w-0 flex-1">
            <Title id={id} title={summary?.title ?? s.title} />
            {/* On a phone, one row that scrolls sideways rather than five that wrap. */}
            <div className="mt-0.5 flex flex-nowrap items-center gap-x-2 gap-y-1 overflow-x-auto text-xs whitespace-nowrap text-muted-foreground [scrollbar-width:none] md:flex-wrap md:overflow-visible md:whitespace-normal">
              <span className="hidden max-w-full truncate font-mono md:inline @max-lg:max-w-48" title={s.root}>
                {s.root}
              </span>
              <SessionSettings
                id={id}
                root={s.root}
                engine={chosen.engine ?? summary?.engine ?? s.engine}
                model={chosen.model ?? summary?.model ?? s.model}
                mode={chosen.mode ?? summary?.mode ?? s.mode}
                onChanged={(p) => setChosen((c) => ({ ...c, ...p }))}
              />
              {s.target && s.target !== "host" && (
                <Badge variant="outline" className="font-normal">
                  {s.target}
                </Badge>
              )}
              <OwnerBadge owner={owner} />
              <CredentialBadge c={summary?.credential ?? s.credential} busy={busy} />
              <span className="hidden md:inline">
                {tokens(s.input_tokens)} in · {tokens(s.output_tokens)} out
              </span>
            </div>
          </div>
          <div className="flex shrink-0 gap-1 md:gap-2">
            {busy && (
              <Button size="sm" variant="destructive" onClick={() => cmd("cancel", {})}>
                <SquareIcon /> Stop
              </Button>
            )}
            <span className="hidden md:contents">
              <LearnNowButton id={id} />
              <Button size="icon-sm" variant="ghost" title="Reload from disk" onClick={() => reload()}>
                <RefreshCwIcon />
              </Button>
              <Link href={"/editor" + qs({ root: s.root })} title="Open the project in the editor" className="grid size-7 place-items-center rounded-md hover:bg-muted">
                <CodeXmlIcon className="size-4" />
              </Link>
            </span>
            <Button size="icon-sm" variant={termOpen ? "secondary" : "ghost"} title="Terminal in this project (Ctrl+`)" onClick={() => toggleTerm()}>
              <SquareTerminalIcon />
            </Button>
            <span className="hidden md:contents">
              <FocusButton />
            </span>
            <PaneToggle side="right" />
          </div>
        </div>

        <div className="relative flex min-h-0 flex-1 flex-col">
          <div className="pointer-events-none absolute top-2 right-4 z-10 md:right-6">
            <Chapters
              messages={s.messages}
              chapters={chapters}
              onJump={(i) => document.getElementById(`m${i}`)?.scrollIntoView({ behavior: "smooth", block: "start" })}
            />
          </div>
          <div ref={scroller} className="min-h-0 flex-1 overflow-auto px-3 py-3 [overflow-anchor:none] md:px-5 md:py-4">
            <div ref={content} className={cn("mx-auto space-y-5", ws.rightOpen ? "max-w-3xl" : "max-w-5xl")}>
              {turns.map((g, gi) => {
                const lastGroup = gi === turns.length - 1;
                if (g.role === "user") {
                  const { m, index } = g.items[0];
                  return (
                    <MessageView
                      key={index}
                      m={m}
                      index={index}
                      onTrace={(idx) => {
                        setTraceFor(idx);
                        setTab("context");
                        ws.openRight();
                      }}
                    />
                  );
                }
                const first = g.items[0].index;
                return (
                  <AssistantRun
                    key={first}
                    items={g.items}
                    since={first > 0 && s.messages[first - 1].role === "user" ? s.messages[first - 1].at : undefined}
                    turnEnd={!lastGroup || !busy}
                    tail={lastGroup && live?.busy ? <LiveTail live={live} attached onApprove={approve} onChoose={choose} /> : undefined}
                  />
                );
              })}
              {(turns[turns.length - 1]?.role !== "assistant" || !live?.busy) && <LiveTail live={live} onApprove={approve} onChoose={choose} />}
            </div>
          </div>
          {below && (
            <Button
              size="sm"
              variant="secondary"
              className="absolute bottom-3 left-1/2 -translate-x-1/2 rounded-full shadow-md"
              onClick={toBottom}
              title="Back to the bottom, and follow from there (End)"
            >
              <ArrowDownIcon /> {busy ? "New output below" : "Jump to latest"}
            </Button>
          )}
        </div>

        <SelectionAction container={content} onAdd={(t) => setQuotes((q) => [...q, t])} />
        <Composer
          session={id}
          root={s.root}
          busy={busy}
          owner={owner}
          target={s.target}
          onSend={send}
          quotes={quotes}
          onQuotes={setQuotes}
          queue={queue}
          onUnqueue={unqueue}
        />
        {termOpen && (
          <BottomDock>
            <TerminalPanel root={s.root} onClose={() => toggleTerm(false)} />
          </BottomDock>
        )}
      </div>

      <RightPane>
        <Tabs value={tab} onValueChange={(v) => setTab(String(v))} className="flex min-h-0 flex-1 flex-col gap-0">
          <div className="border-b px-3 py-2">
            <TabsList>
              <TabsTrigger value="context">Context trace</TabsTrigger>
              <TabsTrigger value="learned">Learned</TabsTrigger>
              <TabsTrigger value="files">Files</TabsTrigger>
              <TabsTrigger value="details">Details</TabsTrigger>
            </TabsList>
          </div>
          <TabsContent value="context" className="min-h-0 flex-1 overflow-auto p-3">
            <TracePanel traces={data.traces} messages={s.messages} focus={traceFor} />
          </TabsContent>
          <TabsContent value="learned" className="min-h-0 flex-1 overflow-auto p-3">
            <LearnedPanel id={id} root={s.root} state={data.learning} count={s.messages.length} />
          </TabsContent>
          <TabsContent value="files" className="flex min-h-0 flex-1 flex-col">
            <Explorer root={s.root} cwd={s.cwd} request={fileReq} />
          </TabsContent>
          <TabsContent value="details" className="min-h-0 flex-1 overflow-auto p-3">
            <DetailsPanel s={s} />
          </TabsContent>
        </Tabs>
      </RightPane>
    </div>
    </MessageHooksContext.Provider>
      </ProjectRoot.Provider>
    </FileOpener.Provider>
  );
}

/** BottomDock holds the terminal under the conversation, its height dragged
 * from its top edge and remembered. */
function BottomDock({ children }: { children: React.ReactNode }) {
  const [h, setH] = React.useState(() => {
    try {
      return Number(localStorage.getItem("agent-tui.term.h")) || 280;
    } catch {
      return 280;
    }
  });
  const drag = (e: React.PointerEvent) => {
    e.preventDefault();
    const y0 = e.clientY;
    const h0 = h;
    let last = h0;
    const move = (ev: PointerEvent) => {
      last = Math.min(Math.max(120, h0 + (y0 - ev.clientY)), Math.round(window.innerHeight * 0.8));
      setH(last);
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      try {
        localStorage.setItem("agent-tui.term.h", String(last));
      } catch {}
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };
  return (
    <div className="relative shrink-0 border-t" style={{ height: h }}>
      <div onPointerDown={drag} className="absolute -top-1 right-0 left-0 z-10 h-2 cursor-row-resize hover:bg-primary/20" title="Drag to resize" />
      {children}
    </div>
  );
}

function Composer({
  session,
  root,
  busy,
  owner,
  target,
  onSend,
  quotes,
  onQuotes,
  queue,
  onUnqueue,
}: {
  session: string;
  root: string;
  busy: boolean;
  owner?: string;
  target?: string;
  onSend: (t: string, now?: boolean, files?: Attached[]) => Promise<void>;
  quotes: string[];
  onQuotes: (q: string[]) => void;
  queue: QueueData;
  onUnqueue: (item: string) => Promise<string | null>;
}) {
  // A prompt forked from ("Fork from here" on it) starts the text, to edit.
  const [text, setText] = React.useState(() => useChatDraft.getState().pending);
  React.useEffect(() => {
    if (useChatDraft.getState().take()) box.current?.focus();
  }, []);
  const [sending, setSending] = React.useState(false);
  // A phone's Enter key is its new line; there the button sends.
  const mobile = useIsMobile();
  // The branch chip asks again when a turn ends: the agent may have moved it.
  const v = useVersion("sessions");
  const box = React.useRef<HTMLTextAreaElement>(null);
  React.useEffect(() => {
    if (quotes.length) box.current?.focus();
  }, [quotes.length]);
  // Images dropped, pasted or picked, sent with the message.
  const att = useAttachments();
  // While a turn runs, Enter queues (the agent gets it after its current
  // step, or when the turn ends); send now stops the turn so the queue goes
  // at once — Claude Code's Enter and Ctrl+Enter.
  const go = async (now = false) => {
    const t = withQuotes(quotes, text);
    if ((!t && !att.items.length && !(now && queue.items.length)) || att.uploading) return;
    setSending(true);
    await onSend(t, now && busy, att.items);
    setSending(false);
    setText("");
    onQuotes([]);
    att.clear();
  };
  // Taking a queued message back to change it: ↑ in an empty box, or edit.
  const takeBack = async (item: string) => {
    const back = await onUnqueue(item);
    if (back !== null) {
      setText((cur) => (cur.trim() ? back + "\n" + cur : back));
      box.current?.focus();
    }
  };
  const canNow = busy && (!!text.trim() || quotes.length > 0 || att.items.length > 0 || queue.items.length > 0);
  return (
    <div className="border-t p-2 md:p-3">
      <div className="mx-auto max-w-3xl">
        {/* Who runs the prompt is a tooltip, not a line of its own: the room
            under the composer goes to the conversation. */}
        <div
          title={
            owner?.startsWith("tui")
              ? "This session is open in a terminal: your prompt runs there, exactly as if typed, and streams here."
              : target && target !== "host"
                ? `No terminal holds this session: the gateway runs it inside ${target}, with the same engines, memory, skills and MCP servers.`
                : "No terminal holds this session: the gateway runs it, with the same engines, memory, skills and MCP servers."
          }
          {...att.dropProps}
          className="relative rounded-xl border bg-background p-1.5 focus-within:ring-[3px] focus-within:ring-ring/30 md:p-2"
        >
          <DropHint show={att.dragging} />
          <AttachmentStrip items={att.items} uploading={att.uploading} onRemove={att.remove} className="mb-1.5 px-1" />
          {queue.items.length > 0 && (
            <div className="mb-1.5 flex flex-col gap-1">
              {queue.items.map((q) => (
                <div key={q.id} className="flex items-start gap-2 rounded-lg border border-dashed px-2 py-1 text-xs text-muted-foreground">
                  <span className="shrink-0 font-medium">queued</span>
                  <span className="line-clamp-2 min-w-0 flex-1 whitespace-pre-wrap">{q.text}</span>
                  <button type="button" title="Take it back to edit (↑ in an empty box)" onClick={() => void takeBack(q.id)} className="shrink-0 rounded px-1 hover:bg-muted hover:text-foreground">
                    edit
                  </button>
                  <button type="button" title="Drop it" onClick={() => void onUnqueue(q.id)} className="shrink-0 rounded px-1 hover:bg-muted hover:text-foreground">
                    ×
                  </button>
                </div>
              ))}
              <div className="px-1 text-[11px] text-muted-foreground">
                {busy
                  ? queue.steers
                    ? "The agent gets these as soon as its running step finishes, within this turn."
                    : "These go when the turn ends."
                  : "Sending…"}
              </div>
            </div>
          )}
          {quotes.length > 0 && (
            <div className="mb-1.5 flex flex-col gap-1">
              {quotes.map((q, i) => (
                <div key={i} className="flex items-start gap-2 rounded-lg border-l-2 border-primary/60 bg-muted/60 py-1 pr-1 pl-2 text-xs text-muted-foreground">
                  <span className="line-clamp-2 min-w-0 flex-1 whitespace-pre-wrap">{q}</span>
                  <button
                    type="button"
                    title="Remove this selection"
                    onClick={() => onQuotes(quotes.filter((_, j) => j !== i))}
                    className="shrink-0 rounded px-1 text-muted-foreground hover:bg-background hover:text-foreground"
                  >
                    ×
                  </button>
                </div>
              ))}
            </div>
          )}
          <div className="flex items-end gap-2">
            <Textarea
              ref={box}
              onPaste={att.onPaste}
              value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.ctrlKey || e.metaKey) && busy) {
                e.preventDefault();
                void go(true);
                return;
              }
              if (e.key === "ArrowUp" && !text && queue.items.length > 0) {
                e.preventDefault();
                void takeBack(queue.items[queue.items.length - 1].id);
                return;
              }
              if (!mobile) sendOnEnter(e, () => void go());
            }}
            placeholder={
              busy
                ? mobile
                  ? queue.steers
                    ? "Steer the agent…"
                    : "Queue a message…"
                  : queue.steers
                    ? "Steer it — the agent gets this after its current step  (Ctrl+Enter: stop and send now)"
                    : "Queue a message — it goes when this turn ends  (Ctrl+Enter: stop and send now)"
                : mobile
                  ? "Reply…"
                  : "Reply…  (Enter to send, Shift+Enter for a new line)"
            }
              className="max-h-48 min-h-10 resize-none border-0 shadow-none focus-visible:ring-0"
            />
            {busy && (
              <Button
                onClick={() => void go(true)}
                disabled={sending || !canNow}
                size="icon"
                variant="outline"
                title="Send now: stop the turn and send (Ctrl+Enter)"
                aria-label="Send now"
              >
                <FastForwardIcon />
              </Button>
            )}
            <Button
              onClick={() => void go()}
              disabled={sending || att.uploading > 0 || (!text.trim() && !quotes.length && !att.items.length)}
              size="icon"
              title={busy ? (queue.steers ? "Queue: the agent gets it after its current step (Enter)" : "Queue: it goes when this turn ends (Enter)") : "Send (Enter)"}
            >
              <SendIcon />
            </Button>
          </div>
          <div className="mt-1 flex items-center gap-2 px-1">
            <AttachButton onPick={(f) => void att.add(f)} />
            <BranchPicker session={session} disabled={busy} version={v} project={projectOf(root)} />
            {quotes.length > 0 && (
              <span className="text-[11px] text-muted-foreground">
                {quotes.length} selection{quotes.length > 1 ? "s" : ""} as context
              </span>
            )}
          </div>
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
      <GraduationCapIcon /> <span className="@max-md:hidden">{busy ? "Learning…" : "Learn now"}</span>
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
    ...(s.credential ? ([["credential", `pool token ${s.credential.slot} of ${s.credential.of} · ${s.credential.id}`]] as [string, React.ReactNode][]) : []),
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

/** SidePlaceholder fills the side panel before a conversation is chosen. */
function SidePlaceholder() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-sm text-muted-foreground">
      <MousePointerClickIcon className="size-5" />
      Once a conversation starts, this panel shows what each turn was given — recalled memories, skills, MCP tools — what it taught the agent, and its
      details.
      <span className="text-xs">Fold it with Alt+] when you want the room.</span>
    </div>
  );
}

/** projectOf is a session's folder as the composer's chip names it. */
function projectOf(root: string) {
  const d = describePath(root);
  return { name: baseName(d.where ? d.dir : root) || root, title: root, where: d.where ? d.where.replace("WSL ", "wsl ") : undefined };
}
