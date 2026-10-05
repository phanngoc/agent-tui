"use client";

import * as React from "react";
import { PowerIcon, RotateCwIcon } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError, gatewayBase } from "@/lib/api";
import { useGateway } from "@/lib/store";
import { Ago, CopyButton, Mono } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

interface Health {
  version: string;
  pid: number;
  started: string;
  peers: number;
  running: string[];
}

// Where from, what to run, what it does; a command is copyable.
const ways: { where: string; run: string; does: string; copy?: boolean }[] = [
  { where: "Start menu", run: "agent-tui web", does: "starts it if needed, opens this page" },
  { where: "a shell", run: "agent-tui web", does: "the same", copy: true },
  { where: "a shell", run: "agent-tui gateway status", does: "also start, stop, restart, log", copy: true },
  { where: "a terminal", run: "/web", does: "opens the session you are in, here" },
];

/**
 * GatewayCard shows the gateway this page runs on, as the service of its own
 * that it is: what it is doing, how to start it without a terminal, and the
 * two things to do to it from here — restart it (after an update) or stop it.
 */
export function GatewayCard() {
  const connected = useGateway((s) => s.connected);
  const [h, setH] = React.useState<Health | null>(null);
  const [state, setState] = React.useState<"" | "restarting" | "stopped">("");

  React.useEffect(() => {
    let live = true;
    const load = () =>
      api
        .get<Health>("/api/health")
        .then((x) => live && setH(x))
        .catch(() => live && setH(null));
    void load();
    const t = setInterval(load, 5000);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [connected]);

  const shutdown = async (params: string) => {
    try {
      await api.post("/api/gateway/shutdown?" + params);
      return true;
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        if (!confirm(`The gateway is running ${h?.running.length ?? "some"} turn(s). Stopping cancels them. Go ahead?`)) return false;
        await api.post("/api/gateway/shutdown?force=1&" + params);
        return true;
      }
      toast.error((e as Error).message);
      return false;
    }
  };

  const restart = async () => {
    const before = h?.pid;
    if (!(await shutdown("restart=1"))) return;
    setState("restarting");
    // The new one comes up on the same address; reload onto it.
    for (let i = 0; i < 60; i++) {
      await new Promise((r) => setTimeout(r, 500));
      try {
        const x = await fetch(gatewayBase() + "/api/health").then((r) => r.json() as Promise<Health>);
        if (x.pid && x.pid !== before) {
          location.reload();
          return;
        }
      } catch {
        // not up yet
      }
    }
    setState("");
    toast.error("The gateway did not come back; start it with: agent-tui gateway start");
  };

  const stop = async () => {
    if (!confirm("Stop the gateway? This page stops working until it is started again, and terminals will not start it on their own until then.")) return;
    if (await shutdown("hold=1")) setState("stopped");
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Gateway
          <span className={connected && state !== "stopped" ? "size-2 rounded-full bg-emerald-500" : "size-2 rounded-full bg-muted-foreground/40"} />
        </CardTitle>
        <CardDescription>
          A service of its own: it serves this page and runs sessions no terminal holds. It keeps running with no terminal or desktop window open, and terminals,
          the desktop app and this page each work without the others.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4 text-sm">
        {state === "stopped" ? (
          <div className="rounded-lg border border-dashed p-3 text-muted-foreground">
            Stopped. Start it again from the Start menu (<b className="text-foreground">agent-tui web</b>) or with <Mono>agent-tui gateway start</Mono>.
          </div>
        ) : state === "restarting" ? (
          <div className="rounded-lg border border-dashed p-3 text-muted-foreground">Restarting… this page reloads when the new one answers.</div>
        ) : h ? (
          <div className="grid grid-cols-[8rem_1fr] gap-y-1.5">
            <span className="text-muted-foreground">address</span>
            <Mono>{gatewayBase() || location.origin}</Mono>
            <span className="text-muted-foreground">process</span>
            <span>
              pid {h.pid}, up since <Ago at={h.started} />
            </span>
            <span className="text-muted-foreground">version</span>
            <Mono className="truncate">{h.version}</Mono>
            <span className="text-muted-foreground">terminals</span>
            <span>{h.peers} connected</span>
            <span className="text-muted-foreground">turns here</span>
            <span>{h.running?.length ?? 0} running</span>
          </div>
        ) : (
          <div className="text-muted-foreground">Not answering.</div>
        )}
        <div className="space-y-1.5">
          <div className="text-xs font-medium text-muted-foreground">Start it, or this page, without a terminal</div>
          {ways.map((w) => (
            <div key={w.where + w.run} className="flex min-w-0 items-center gap-3">
              <span className="w-24 shrink-0 text-muted-foreground">{w.where}</span>
              <Mono className="shrink-0">{w.run}</Mono>
              {w.copy && <CopyButton text={w.run} />}
              <span className="truncate text-xs text-muted-foreground">{w.does}</span>
            </div>
          ))}
        </div>
        {state === "" && h && (
          <div className="flex gap-2">
            <Button size="sm" variant="outline" onClick={restart} title="Stop it and start it again from the installed binary — after updating agent-tui">
              <RotateCwIcon /> Restart
            </Button>
            <Button size="sm" variant="outline" onClick={stop}>
              <PowerIcon /> Stop
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
