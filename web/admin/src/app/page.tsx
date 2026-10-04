"use client";

import Link from "next/link";
import { ActivityIcon, BrainIcon, RadioIcon, TerminalIcon } from "lucide-react";
import { useGateway, useVersion } from "@/lib/store";
import { useFetch } from "@/lib/hooks";
import { qs } from "@/lib/api";
import type { Activity, LearnStatus, MemoryStats, Peer, Project, Summary, Skill, McpServer } from "@/lib/types";
import { Ago, Dot, Empty, PageHeader, Stat } from "@/components/common";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { ActivityItem } from "@/components/learn-feed";
import { baseName } from "@/lib/format";
import { OwnerBadge } from "@/components/owner-badge";

interface Overview {
  version: string;
  pid: number;
  addr: string;
  started: string;
  peers: Peer[];
  sessions: number;
  busy: number;
  recent: Summary[];
  projects: Project[];
  learner: LearnStatus;
  activity: Activity[];
  paths: Record<string, string>;
}

export default function OverviewPage() {
  const root = useGateway((s) => s.root);
  const live = useGateway((s) => s.live);
  const feed = useGateway((s) => s.feed);
  const v = useVersion("sessions", "learn", "peers");
  const vm = useVersion("memory", "skills", "mcp");
  const { data } = useFetch<Overview>("/api/overview", [v]);
  const { data: mem } = useFetch<{ stats: MemoryStats[] }>("/api/memory" + qs({ root, q: "-" }), [vm, root]);
  const { data: skills } = useFetch<{ skills: Skill[] }>("/api/skills" + qs({ root }), [vm, root]);
  const { data: mcp } = useFetch<{ servers: McpServer[] }>("/api/mcp" + qs({ root }), [vm, root]);

  const running = Object.values(live).filter((l) => l.busy);
  const titles = Object.fromEntries((data?.recent ?? []).map((s) => [s.id, s.title]));
  const records = (mem?.stats ?? []).reduce((n, s) => n + s.records, 0);

  return (
    <div>
      <PageHeader
        title="Overview"
        description={
          data ? (
            <>
              Gateway {data.version} on <code className="font-mono">{data.addr}</code>, pid {data.pid}, up since <Ago at={data.started} />
            </>
          ) : (
            "Connecting to the gateway…"
          )
        }
      />
      <div className="grid gap-4 p-6">
        <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
          <Stat label="Sessions" value={data?.sessions ?? "–"} hint="saved on this machine" />
          <Stat label="Running now" value={running.length} hint="turns in flight, anywhere" />
          <Stat label="Terminals" value={data?.peers.length ?? "–"} hint="connected to the gateway" />
          <Stat
            label="Memories"
            value={records}
            hint={(mem?.stats ?? []).map((s) => `${s.records} ${s.scope}`).join(" · ") || "global + project"}
          />
          <Stat label="Skills" value={(skills?.skills ?? []).filter((s) => !s.shadowed).length} hint={`${(skills?.skills ?? []).filter((s) => s.learned).length} learned`} />
          <Stat label="MCP servers" value={(mcp?.servers ?? []).filter((s) => !s.shadowed).length} hint="global + project" />
        </div>

        <div className="grid gap-4 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <ActivityIcon className="size-4" /> Running now
              </CardTitle>
              <CardDescription>Every turn in flight, whether a terminal or the gateway runs it.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-2">
              {running.length === 0 && <Empty title="Nothing is running">Start a conversation from Sessions, or in a terminal.</Empty>}
              {running.map((l) => (
                <Link key={l.session} href={`/sessions?id=${l.session}`} className="flex items-center gap-3 rounded-lg border p-3 hover:bg-muted/50">
                  <Dot on pulse />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-medium">{titles[l.session] ?? l.prompt ?? l.session}</div>
                    <div className="truncate text-xs text-muted-foreground">
                      {l.status} · {l.engine} · {Object.keys(l.approvals).length > 0 && <b className="text-amber-600">waiting for approval · </b>}
                      <Ago at={l.started} />
                    </div>
                  </div>
                  <OwnerBadge owner={l.owner} />
                </Link>
              ))}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <TerminalIcon className="size-4" /> Terminals
              </CardTitle>
              <CardDescription>A terminal holds the sessions it has open: prompts sent here for those go to it.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-2">
              {(data?.peers ?? []).length === 0 && <Empty title="No terminal attached">Open <code>tui</code> anywhere; it connects on its own.</Empty>}
              {(data?.peers ?? []).map((p) => (
                <div key={p.id} className="rounded-lg border p-3">
                  <div className="flex items-center gap-2 text-sm">
                    <Dot on />
                    <span className="font-medium">{baseName(p.root)}</span>
                    <span className="truncate font-mono text-xs text-muted-foreground">{p.root}</span>
                  </div>
                  <div className="mt-1 text-xs text-muted-foreground">
                    {p.id} · pid {p.pid} · joined <Ago at={p.joined} /> · holds {p.sessions?.length ?? 0} sessions
                  </div>
                </div>
              ))}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <BrainIcon className="size-4" /> Learning
              </CardTitle>
              <CardDescription>
                {data?.learner.error ? (
                  <span className="text-destructive">{data.learner.error}</span>
                ) : (
                  <>
                    Model <b>{data?.learner.model ?? "…"}</b> · queue {data?.learner.queue ?? 0}
                    {data?.learner.busy && <> · working on {data.learner.busy}</>}
                  </>
                )}
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-1">
              {(data?.activity ?? []).length === 0 && <Empty title="Nothing learned yet">After a few turns, what was worth keeping shows up here.</Empty>}
              {(data?.activity ?? []).slice(0, 8).map((a, i) => (
                <ActivityItem key={i} a={a} compact />
              ))}
              <Link href="/memory?tab=learning" className="block pt-2 text-xs text-muted-foreground hover:text-foreground">
                All learning activity →
              </Link>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <RadioIcon className="size-4" /> Event stream
              </CardTitle>
              <CardDescription>What the gateway is relaying right now (deltas omitted).</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="max-h-72 space-y-1 overflow-auto font-mono text-[11px]">
                {feed.length === 0 && <div className="text-muted-foreground">waiting for events…</div>}
                {feed.slice(0, 80).map((e) => (
                  <div key={e.seq} className="flex gap-2">
                    <span className="w-10 shrink-0 text-right text-muted-foreground">{e.seq}</span>
                    <span className="w-28 shrink-0">{e.type}</span>
                    <span className="w-28 shrink-0 truncate text-muted-foreground">{e.origin}</span>
                    {e.session && (
                      <Link className="truncate text-muted-foreground hover:underline" href={`/sessions?id=${e.session}`}>
                        {e.session}
                      </Link>
                    )}
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Recent sessions</CardTitle>
          </CardHeader>
          <CardContent className="divide-y">
            {(data?.recent ?? []).map((s) => (
              <Link key={s.id} href={`/sessions?id=${s.id}`} className="flex items-center gap-3 py-2 hover:bg-muted/40">
                <Dot on={s.busy} pulse />
                <span className="min-w-0 flex-1 truncate text-sm">{s.title}</span>
                <span className="hidden truncate text-xs text-muted-foreground md:block">{baseName(s.root)}</span>
                <Badge variant="outline" className="font-normal">
                  {s.engine || "api"}
                </Badge>
                <Ago at={s.updated} className="w-20 text-right text-xs" />
              </Link>
            ))}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
