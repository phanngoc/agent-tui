"use client";

import * as React from "react";
import { CoffeeIcon } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useVersion } from "@/lib/store";
import { Ago, Field, Mono, NativeSelect } from "@/components/common";
import { Switch } from "@/components/ui/switch";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export interface AwakeStatus {
  mode: string;
  display: boolean;
  active: boolean;
  reason?: string;
  since?: string;
  supported: boolean;
  method: string;
  error?: string;
}

const MODES = [
  { value: "off", label: "off — the computer sleeps as usual" },
  { value: "busy", label: "while the agent works" },
  { value: "schedules", label: "while working, or a schedule is on" },
  { value: "always", label: "always, while the gateway runs" },
];

/** KeepAwakeCard is caffeinate for agent-tui: keep the computer from sleeping while the agent works. */
export function KeepAwakeCard() {
  const v = useVersion("settings");
  const { data, reload } = useFetch<AwakeStatus>("/api/awake", [v]);
  const [busy, setBusy] = React.useState(false);
  // The state changes as turns start and end: ask again now and then.
  React.useEffect(() => {
    const t = setInterval(reload, 10000);
    return () => clearInterval(t);
  }, [reload]);
  if (!data) return null;
  const save = async (patch: { keep_awake?: string; keep_display?: boolean }) => {
    setBusy(true);
    try {
      await api.put("/api/settings/global", patch);
      reload();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <CoffeeIcon className="size-4" /> Keep awake
          <span className={`text-sm font-normal ${data.active ? "text-emerald-600 dark:text-emerald-400" : "text-muted-foreground"}`}>
            · {data.active ? "holding the computer awake" : "not holding"}
          </span>
        </CardTitle>
        <CardDescription>
          Like caffeinate: a long task or an hourly schedule is no use on a computer that sleeps after a few idle minutes. Closing the lid or choosing Sleep still sleeps.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3">
        <Field label="Keep the computer awake" hint={data.mode === "schedules" ? "Scheduled jobs only run while the computer is awake." : undefined}>
          <NativeSelect value={data.mode} disabled={busy || !data.supported} onChange={(m) => void save({ keep_awake: m })} options={MODES} />
        </Field>
        <label className="flex items-center gap-2 text-sm">
          <Switch checked={data.display} disabled={busy || !data.supported || data.mode === "off"} onCheckedChange={(on) => void save({ keep_display: on })} />
          Keep the screen on too
        </label>
        <div className="text-xs text-muted-foreground">
          {!data.supported ? (
            <>Not available here: {data.method}.</>
          ) : data.active ? (
            <>
              Holding since <Ago at={data.since} /> — {data.reason}. Through <Mono>{data.method}</Mono>.
            </>
          ) : (
            <>
              Nothing to hold for now. Through <Mono>{data.method}</Mono> when there is.
            </>
          )}
          {data.error && <div className="mt-1 text-red-600">{data.error}</div>}
        </div>
      </CardContent>
    </Card>
  );
}
