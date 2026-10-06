"use client";

import * as React from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useVersion } from "@/lib/store";
import { Ago, ErrorNote, Field, Mono, NativeSelect } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

interface Person {
  id: number;
  name?: string;
  username?: string;
  since?: string;
}
interface Pairing {
  code: string;
  id: number;
  name?: string;
  username?: string;
  created: string;
}
interface TelegramView {
  enabled: boolean;
  has_token: boolean;
  dm_policy: string;
  allowed: Person[];
  pending: Pairing[];
  quiet_schedules: boolean;
  streaming: string;
  status: { state: string; username?: string; error?: string };
}

const TONE: Record<string, string> = {
  up: "text-emerald-600 dark:text-emerald-400",
  starting: "text-amber-600 dark:text-amber-400",
  error: "text-red-600 dark:text-red-400",
};

/** TelegramCard sets up the Telegram bot: its token, who may use it, and the pairing requests waiting. */
export function TelegramCard() {
  const v = useVersion("remote");
  const { data, error, reload } = useFetch<TelegramView>("/api/telegram", [v]);
  const [token, setToken] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const act = async (f: () => Promise<unknown>, ok?: string) => {
    setBusy(true);
    try {
      await f();
      if (ok) toast.success(ok);
      reload();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  if (error) return <ErrorNote error={error} />;
  if (!data) return null;
  const st = data.status;
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Telegram bot
          <span className={`text-sm font-normal ${TONE[st.state] ?? "text-muted-foreground"}`}>
            · {st.state === "up" ? "online" : st.state}
          </span>
        </CardTitle>
        <CardDescription>
          Talk to a project&apos;s agent from Telegram, approve what it asks with a tap, and get what the schedules found. Make a bot with{" "}
          <a className="text-sky-600 hover:underline dark:text-sky-400" href="https://t.me/BotFather" target="_blank" rel="noreferrer">
            @BotFather
          </a>{" "}
          (/newbot) and paste its token.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3">
        {st.error && <ErrorNote error={st.error} />}
        {st.username && (
          <p className="text-sm">
            Open{" "}
            <a className="font-medium text-sky-600 hover:underline dark:text-sky-400" href={`https://t.me/${st.username}`} target="_blank" rel="noreferrer">
              @{st.username}
            </a>{" "}
            and send it anything: it answers with a pairing code to approve here.
          </p>
        )}
        <Field label="Bot token" hint={data.has_token ? "One is saved in telegram.json on this computer; paste another to replace it." : "Like 123456789:AA… — kept on this computer, readable by you only."}>
          <div className="flex gap-2">
            <Input type="password" value={token} onChange={(e) => setToken(e.target.value)} placeholder={data.has_token ? "•••••••• saved" : "paste the token"} autoComplete="off" />
            <Button
              disabled={busy || !token.trim()}
              onClick={() =>
                act(async () => {
                  await api.put("/api/telegram", { token: token.trim(), enabled: true });
                  setToken("");
                }, "Token saved; the bot is starting")
              }
            >
              Save
            </Button>
          </div>
        </Field>
        <Field
          label="While the agent works"
          hint={
            data.streaming === "partial"
              ? "The answer streams into one message as it is written."
              : data.streaming === "block"
                ? "One message of progress, and what the agent says between steps as messages of their own."
                : data.streaming === "off"
                  ? "Only the typing indicator, then the answer."
                  : "One message of progress — what it is thinking, each step with ⏳/✅/❌ and its time — that folds into a summary; then the answer."
          }
        >
          <NativeSelect
            value={data.streaming}
            onChange={(m) => act(() => api.put("/api/telegram", { streaming: m }))}
            options={[
              { value: "progress", label: "progress — steps as they happen (default)" },
              { value: "partial", label: "partial — stream the answer" },
              { value: "block", label: "block — progress, and each thing it says" },
              { value: "off", label: "off — just the answer" },
            ]}
          />
        </Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Who may use it" hint={data.dm_policy === "pairing" ? "Strangers get a code to approve here." : data.dm_policy === "allowlist" ? "Only those approved; strangers get no answer." : "Nobody."}>
            <NativeSelect
              value={data.dm_policy}
              onChange={(p) => act(() => api.put("/api/telegram", { dm_policy: p }))}
              options={[
                { value: "pairing", label: "pairing code" },
                { value: "allowlist", label: "approved only" },
                { value: "disabled", label: "nobody" },
              ]}
            />
          </Field>
          <div className="grid content-start gap-2 pt-1 text-sm">
            <label className="flex items-center gap-2">
              <Switch checked={data.enabled} disabled={busy || (!data.has_token && !data.enabled)} onCheckedChange={(on) => act(() => api.put("/api/telegram", { enabled: on }))} />
              Bot running
            </label>
            <label className="flex items-center gap-2">
              <Switch checked={!data.quiet_schedules} disabled={busy} onCheckedChange={(on) => act(() => api.put("/api/telegram", { quiet_schedules: !on }))} />
              Send schedule reports
            </label>
          </div>
        </div>

        {data.pending.length > 0 && (
          <div className="grid gap-2">
            <div className="text-xs font-medium text-muted-foreground uppercase">Waiting for approval</div>
            {data.pending.map((p) => (
              <div key={p.code} className="flex flex-wrap items-center gap-2 rounded-lg border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-sm">
                <Mono className="text-base font-semibold tracking-widest">{p.code}</Mono>
                <span className="min-w-0 truncate">
                  {p.name} {p.username && <span className="text-muted-foreground">@{p.username}</span>} <span className="text-xs text-muted-foreground">· {p.id}</span>
                </span>
                <Ago at={p.created} className="text-xs text-muted-foreground" />
                <span className="ml-auto flex gap-1">
                  <Button size="sm" disabled={busy} onClick={() => act(() => api.post("/api/telegram/approve", { code: p.code }), `${p.name || p.id} may use the bot`)}>
                    Approve
                  </Button>
                  <Button size="sm" variant="ghost" disabled={busy} onClick={() => act(() => api.post("/api/telegram/reject", { code: p.code }))}>
                    Reject
                  </Button>
                </span>
              </div>
            ))}
          </div>
        )}

        <div className="grid gap-1.5">
          <div className="text-xs font-medium text-muted-foreground uppercase">Allowed</div>
          {data.allowed.length === 0 ? (
            <p className="text-sm text-muted-foreground">Nobody yet.</p>
          ) : (
            data.allowed.map((p) => (
              <div key={p.id} className="flex items-center gap-2 text-sm">
                <span className="min-w-0 truncate">
                  {p.name} {p.username && <span className="text-muted-foreground">@{p.username}</span>}
                </span>
                <span className="text-xs text-muted-foreground">{p.id}</span>
                <Button
                  size="sm"
                  variant="ghost"
                  className="ml-auto text-red-600"
                  disabled={busy}
                  onClick={() => {
                    if (confirm(`Take ${p.name || p.id} off the bot?`)) void act(() => api.post("/api/telegram/revoke", { id: p.id }));
                  }}
                >
                  Remove
                </Button>
              </div>
            ))
          )}
        </div>
        {st.state === "up" && data.allowed.length > 0 && (
          <div>
            <Button variant="outline" size="sm" disabled={busy} onClick={() => act(() => api.post("/api/telegram/test"), "Sent")}>
              Send a test message
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
