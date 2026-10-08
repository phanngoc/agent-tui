"use client";

import * as React from "react";
import Link from "next/link";
import { ExternalLinkIcon, Loader2Icon, RotateCcwIcon, SquareIcon, XIcon } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { useGateway } from "@/lib/store";
import { useLiveSession } from "@/lib/live-session";
import type { Session } from "@/lib/types";
import { AnchorPrefix, AssistantRun, LiveTail, MessageView, groupTurns } from "@/components/transcript";
import { MessageHooksContext } from "@/components/message-actions";
import { systemTone } from "@/components/commands";

/**
 * BtwPanel is a conversation's side chat, over its composer: what was asked
 * with /btw and what came back, while the conversation's own turn goes on.
 * The side chat is a fork — it knows everything said up to when it was made
 * — but nothing in it reaches the conversation.
 */
export function BtwPanel({ side, parentCount, onClose, onReset }: { side: string; parentCount: number; onClose: () => void; onReset: () => void }) {
  const { data, error } = useLiveSession<{ session: Session }>(side);
  const live = useGateway((s) => s.live[side]);
  const busy = live?.busy ?? false;
  const s = data?.session;
  const from = s?.side_from ?? 0;
  // The conversation has moved on since this was forked: the next /btw is
  // asked of a fresh one that knows what came after.
  const stale = !!s && parentCount > from && !busy;
  const turns = React.useMemo(() => {
    const msgs = s?.messages ?? [];
    return groupTurns(msgs.slice(from)).map((g) => ({
      ...g,
      items: g.items.map((it) => ({ ...it, index: it.index + from })),
    }));
  }, [s?.messages, from]);
  const scroller = React.useRef<HTMLDivElement>(null);
  // The answer is what you are waiting for: keep the bottom in view.
  React.useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [turns, live?.partial, live?.status]);
  const post = (path: string, body: unknown) => api.post(`/api/sessions/${side}/${path}`, body).catch((e: Error) => toast.error(e.message));
  const approve = (id: string, verdict: "allow" | "allow_all" | "deny") => void post("approve", { id, verdict });
  const choose = (id: string, index: number) => void post("choose", { id, index });
  const last = turns[turns.length - 1];

  return (
    <div className="mx-auto mb-2 flex max-h-[45vh] max-w-3xl flex-col overflow-hidden rounded-xl border border-violet-500/40 bg-violet-500/[0.03] shadow-sm">
      <div className="flex items-center gap-2 border-b border-violet-500/20 px-3 py-1.5 text-xs">
        <span className={cn("rounded px-1 font-mono font-medium", systemTone)}>/btw</span>
        <span className="min-w-0 truncate text-muted-foreground">
          {stale ? "Asked earlier — the next /btw starts afresh, knowing everything since" : "Side chat — knows this conversation so far; the agent isn't interrupted and never sees it"}
        </span>
        <div className="ml-auto flex shrink-0 items-center gap-0.5 text-muted-foreground">
          {busy && <Loader2Icon className="mr-1 size-3.5 animate-spin text-violet-500" />}
          {busy && (
            <button type="button" title="Stop answering" onClick={() => void post("cancel", {})} className="rounded p-1 hover:bg-muted hover:text-foreground">
              <SquareIcon className="size-3.5" />
            </button>
          )}
          <button type="button" title="Start over: the next /btw forks the conversation afresh" onClick={onReset} className="rounded p-1 hover:bg-muted hover:text-foreground">
            <RotateCcwIcon className="size-3.5" />
          </button>
          <Link href={`/sessions?id=${side}`} title="Open the side chat as a conversation" className="rounded p-1 hover:bg-muted hover:text-foreground">
            <ExternalLinkIcon className="size-3.5" />
          </Link>
          <button type="button" title="Hide (Esc in an empty box) — /btw brings it back" onClick={onClose} className="rounded p-1 hover:bg-muted hover:text-foreground">
            <XIcon className="size-3.5" />
          </button>
        </div>
      </div>
      <div ref={scroller} className="min-h-0 flex-1 overflow-auto px-3 py-2">
        {error ? (
          <div className="text-xs text-destructive">{error}</div>
        ) : !s ? (
          <div className="text-xs text-muted-foreground">Loading…</div>
        ) : (
          <AnchorPrefix.Provider value="btw-m">
            <MessageHooksContext.Provider value={null}>
              <div className="space-y-3 text-sm">
                {turns.length === 0 && !busy && (
                  <div className="py-2 text-xs text-muted-foreground">
                    Type <span className="font-mono text-violet-700 dark:text-violet-300">/btw your question</span> in the box below. The answer shows here.
                  </div>
                )}
                {turns.map((g, gi) => {
                  if (g.role === "user") {
                    const { m, index } = g.items[0];
                    return <MessageView key={index} m={m} index={index} />;
                  }
                  const lastGroup = gi === turns.length - 1;
                  const first = g.items[0].index;
                  return (
                    <AssistantRun
                      key={first}
                      items={g.items}
                      since={first > 0 && s.messages[first - 1].role === "user" ? s.messages[first - 1].at : undefined}
                      turnEnd={!lastGroup || !busy}
                      tail={lastGroup && busy ? <LiveTail live={live} attached onApprove={approve} onChoose={choose} /> : undefined}
                    />
                  );
                })}
                {(last?.role !== "assistant" || !busy) && <LiveTail live={live} onApprove={approve} onChoose={choose} />}
              </div>
            </MessageHooksContext.Provider>
          </AnchorPrefix.Provider>
        )}
      </div>
    </div>
  );
}
