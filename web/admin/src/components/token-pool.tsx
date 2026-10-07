"use client";

import * as React from "react";
import Link from "next/link";
import { KeyRoundIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { useFetch } from "@/lib/hooks";
import { tokens } from "@/lib/format";
import type { Credential } from "@/lib/types";
import { Ago, Mono } from "@/components/common";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

interface Tally {
  turns: number;
  in: number;
  out: number;
  cache_read: number;
  cache_write: number;
}

interface Limit {
  window: string;
  used: number; // 0..1
  resets: string;
}

interface PoolToken extends Credential {
  conversations: number;
  today: Tally;
  week: Tally;
  total: Tally;
  last_used?: string;
  limits?: Limit[];
  limits_at?: string;
}

interface PoolReport {
  enabled: boolean;
  size: number;
  tokens: PoolToken[];
}

/** usePool reads the pool's record, again every minute: turns elsewhere spend from it too. */
function usePool(deps: unknown[] = []) {
  const r = useFetch<PoolReport>("/api/token-pool", deps);
  const { reload } = r;
  React.useEffect(() => {
    const t = setInterval(reload, 60000);
    return () => clearInterval(t);
  }, [reload]);
  return r;
}

const windowLabel: Record<string, string> = {
  five_hour: "5h",
  seven_day: "7d",
  seven_day_opus: "7d Opus",
  seven_day_sonnet: "7d Sonnet",
};

/** until says how long until a time to come, the way timeAgo says how long since. */
function until(iso: string): string {
  const s = (new Date(iso).getTime() - Date.now()) / 1000;
  if (s <= 0) return "now";
  if (s < 3600) return `${Math.max(1, Math.round(s / 60))}m`;
  if (s < 86400) return `${Math.round(s / 3600)}h`;
  return `${Math.round(s / 86400)}d`;
}

/** live is a window's share as it stands now: once it has reset, what was spent is gone. */
function live(l: Limit): number {
  return l.resets && new Date(l.resets).getTime() <= Date.now() ? 0 : l.used;
}

function tone(used: number) {
  return used >= 0.9 ? "bg-red-500" : used >= 0.7 ? "bg-amber-500" : "bg-primary";
}

/** fresh is what a turn sent that was not already cached: the part the allowance weighs most. */
const fresh = (t: Tally) => t.in + t.cache_write;

function tallyTitle(t: Tally) {
  return `${t.turns} turns · ${t.in.toLocaleString()} input · ${t.cache_write.toLocaleString()} cache writes · ${t.cache_read.toLocaleString()} cache reads · ${t.out.toLocaleString()} output`;
}

/**
 * CredentialBadge says which token of the Claude pool a conversation runs on,
 * and how much of that token's five-hour allowance is spent.
 */
export function CredentialBadge({ c, busy }: { c?: Credential; busy?: boolean }) {
  // A finished turn has spent something: read the record again then.
  const { data } = usePool([c?.id, busy]);
  if (!c) return null;
  const t = data?.tokens.find((x) => x.id === c.id);
  const five = t?.limits?.find((l) => l.window === "five_hour");
  const used = five ? live(five) : undefined;
  return (
    <Link href="/settings#token-pool" title={`Claude pool token ${c.slot} of ${c.of} · ${c.id}`}>
      <Badge variant="outline" className="gap-1 font-normal hover:bg-muted">
        <KeyRoundIcon className="size-3" />
        token {c.slot}/{c.of}
        {used !== undefined && (
          <span className={cn(used >= 0.9 ? "text-red-600 dark:text-red-400" : used >= 0.7 ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground")}>
            · {Math.round(used * 100)}% 5h
          </span>
        )}
      </Badge>
    </Link>
  );
}

function LimitBar({ l }: { l: Limit }) {
  const used = live(l);
  return (
    <div className="grid grid-cols-[4.5rem_1fr_2.5rem] items-center gap-2 text-xs" title={`resets ${new Date(l.resets).toLocaleString()}`}>
      <span className="text-muted-foreground">{windowLabel[l.window] ?? l.window}</span>
      <div className="h-1.5 overflow-hidden rounded-full bg-muted">
        <div className={cn("h-full rounded-full", tone(used))} style={{ width: `${Math.min(100, used * 100)}%` }} />
      </div>
      <span className="text-right tabular-nums">{Math.round(used * 100)}%</span>
      {used > 0 && <span className="col-start-2 -mt-1 text-[11px] text-muted-foreground">resets in {until(l.resets)}</span>}
    </div>
  );
}

function Spent({ label, t }: { label: string; t: Tally }) {
  return (
    <div title={tallyTitle(t)}>
      <div className="text-[11px] text-muted-foreground">{label}</div>
      <div className="tabular-nums">
        {tokens(fresh(t))} in · {tokens(t.out)} out
      </div>
      <div className="text-[11px] text-muted-foreground tabular-nums">
        {t.turns} turn{t.turns === 1 ? "" : "s"} · {tokens(t.cache_read)} cached
      </div>
    </div>
  );
}

/** TokenPoolCard follows each token of the Claude pool: what it has spent, and what is left of its allowance. */
export function TokenPoolCard() {
  const { data } = usePool();
  if (!data) return null;
  return (
    <Card id="token-pool" className="xl:col-span-2">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <KeyRoundIcon className="size-4" /> Claude token pool
          {data.enabled && <span className="text-sm font-normal text-muted-foreground">· {data.size} tokens</span>}
        </CardTitle>
        <CardDescription>
          {data.enabled ? (
            <>
              Each conversation keeps the token it was given, in turn, from <Mono>~/.claude/token-rotation/pool.xml</Mono>. Spending is counted from the turns agent-tui ran; the allowance is
              what Claude last reported for that token.
            </>
          ) : (
            <>
              No pool: there is no <Mono>~/.claude/token-rotation/pool.xml</Mono>, so Claude Code runs on its own login.
            </>
          )}
        </CardDescription>
      </CardHeader>
      {data.enabled && (
        <CardContent className="grid gap-3 md:grid-cols-2 2xl:grid-cols-3">
          {data.tokens.map((t) => (
            <div key={t.id || `slot-${t.slot}`} className={cn("grid gap-3 rounded-lg border p-3 text-sm", !t.id && "text-muted-foreground")}>
              <div className="flex items-center gap-2">
                <Badge variant="secondary" className="font-normal">
                  #{t.slot}
                </Badge>
                {t.id ? <Mono className="text-xs">{t.id}</Mono> : <span className="text-xs">not used yet</span>}
                {t.id && t.of !== data.size && <span className="text-xs text-amber-600 dark:text-amber-400">no longer in the pool?</span>}
                <span className="ml-auto text-xs text-muted-foreground">
                  {t.last_used ? <Ago at={t.last_used} /> : null}
                </span>
              </div>
              {t.id && (
                <>
                  <div className="grid gap-1.5">
                    {t.limits?.length ? (
                      t.limits.map((l) => <LimitBar key={l.window} l={l} />)
                    ) : (
                      <span className="text-xs text-muted-foreground">No allowance reported yet.</span>
                    )}
                  </div>
                  <div className="grid grid-cols-3 gap-2 text-xs">
                    <Spent label="Today" t={t.today} />
                    <Spent label="7 days" t={t.week} />
                    <Spent label="All" t={t.total} />
                  </div>
                  <div className="text-xs text-muted-foreground">
                    {t.conversations} conversation{t.conversations === 1 ? "" : "s"}
                    {t.limits_at && (
                      <>
                        {" "}
                        · allowance as of <Ago at={t.limits_at} />
                      </>
                    )}
                  </div>
                </>
              )}
            </div>
          ))}
        </CardContent>
      )}
    </Card>
  );
}
