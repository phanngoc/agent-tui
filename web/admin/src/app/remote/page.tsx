"use client";

import * as React from "react";
import QRCode from "qrcode";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useVersion } from "@/lib/store";
import { CopyButton, ErrorNote, Field, Mono, NativeSelect, PageHeader, Pre } from "@/components/common";
import { TelegramCard } from "@/components/telegram-card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

interface RemoteView {
  enabled: boolean;
  remote: boolean;
  tunnel: { mode?: string; hostname?: string; auto_start?: boolean; has_token: boolean; has_api_token: boolean };
  status: { state: string; url?: string; error?: string; log?: string[]; since?: string; mode?: string };
  key?: string;
  login_url?: string;
}

export default function RemotePage() {
  const v = useVersion("remote");
  const { data, error, reload } = useFetch<RemoteView>("/api/remote", [v]);
  return (
    <div>
      <PageHeader
        title="Remote & Telegram"
        description="Run the gateway from your phone: through a Cloudflare tunnel, signed in with a key only this computer shows, or by talking to a Telegram bot."
      />
      <div className="grid gap-4 p-4 md:gap-6 md:p-6 xl:grid-cols-2">
        {error && <ErrorNote error={error} />}
        {data && <TunnelCard data={data} reload={reload} />}
        {data && !data.remote && <PhoneCard data={data} reload={reload} />}
        {data?.remote && <SignedInCard />}
        <TelegramCard />
      </div>
    </div>
  );
}

const STATE: Record<string, { label: string; tone: string }> = {
  off: { label: "off", tone: "" },
  installing: { label: "downloading cloudflared…", tone: "text-amber-600 dark:text-amber-400" },
  starting: { label: "connecting…", tone: "text-amber-600 dark:text-amber-400" },
  up: { label: "online", tone: "text-emerald-600 dark:text-emerald-400" },
  error: { label: "failed", tone: "text-red-600 dark:text-red-400" },
};

function TunnelCard({ data, reload }: { data: RemoteView; reload: () => void }) {
  const [mode, setMode] = React.useState(data.tunnel.mode || (data.tunnel.has_api_token ? "api" : "quick"));
  const [hostname, setHostname] = React.useState(data.tunnel.hostname ?? "");
  const [apiToken, setApiToken] = React.useState("");
  const [token, setToken] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const st = data.status;
  const running = st.state === "up" || st.state === "starting" || st.state === "installing";
  const local = !data.remote;

  const save = async (extra?: Record<string, unknown>) => {
    await api.put("/api/remote", { mode, hostname, api_token: apiToken || undefined, token: token || undefined, ...extra });
    setApiToken("");
    setToken("");
  };
  const act = async (f: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await f();
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
          Cloudflare tunnel
          <span className={`text-sm font-normal ${STATE[st.state]?.tone ?? ""}`}>· {STATE[st.state]?.label ?? st.state}</span>
        </CardTitle>
        <CardDescription>
          cloudflared connects out from this computer, so nothing needs opening on the router. It is downloaded the first time it is needed.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3">
        {st.url && (
          <div className="flex items-center gap-2 rounded-lg border bg-muted/40 px-3 py-2">
            <a href={st.url} target="_blank" rel="noreferrer" className="min-w-0 truncate font-mono text-sm text-sky-600 hover:underline dark:text-sky-400">
              {st.url}
            </a>
            <CopyButton text={st.url} className="ml-auto" />
          </div>
        )}
        {st.error && <ErrorNote error={st.error} />}
        <Field
          label="Address"
          hint={
            mode === "quick"
              ? "A new https://….trycloudflare.com address each start; no account. Live updates still work (they stream by POST)."
              : mode === "api"
                ? "Your own hostname, set up by agent-tui with a Cloudflare API token: the tunnel, its route and the DNS record."
                : "A tunnel you made in the Cloudflare dashboard (Zero Trust → Networks → Tunnels), with its public hostname pointing at this gateway."
          }
        >
          <NativeSelect
            value={mode}
            onChange={setMode}
            disabled={!local}
            options={[
              { value: "api", label: "my domain — API token" },
              { value: "token", label: "my domain — tunnel token" },
              { value: "quick", label: "quick — trycloudflare.com" },
            ]}
          />
        </Field>
        {mode !== "quick" && (
          <Field label="Hostname" hint={mode === "api" ? "A name nothing else uses, in one of the token's zones." : "The public hostname you gave the tunnel."}>
            <Input value={hostname} onChange={(e) => setHostname(e.target.value)} placeholder="agent.example.com" disabled={!local} autoCapitalize="off" />
          </Field>
        )}
        {mode === "api" && (
          <Field
            label="Cloudflare API token"
            hint={
              <>
                Needs <Mono>Account · Cloudflare Tunnel · Edit</Mono> and <Mono>Zone · DNS · Edit</Mono>.{" "}
                {data.tunnel.has_api_token ? "One is saved; paste another to replace it." : "Kept in remote.json on this computer, readable by you only."}
              </>
            }
          >
            <Input type="password" value={apiToken} onChange={(e) => setApiToken(e.target.value)} placeholder={data.tunnel.has_api_token ? "•••••••• saved" : "paste the token"} disabled={!local} />
          </Field>
        )}
        {mode === "token" && (
          <Field label="Tunnel token" hint={data.tunnel.has_token ? "One is saved; paste another to replace it." : "From the tunnel's install command: the long eyJ… string."}>
            <Input type="password" value={token} onChange={(e) => setToken(e.target.value)} placeholder={data.tunnel.has_token ? "•••••••• saved" : "eyJ…"} disabled={!local} />
          </Field>
        )}
        <label className="flex items-center gap-2 text-sm">
          <Switch checked={!!data.tunnel.auto_start} disabled={!local} onCheckedChange={(on) => act(() => api.put("/api/remote", { auto_start: on }))} />
          Start the tunnel with the gateway
        </label>
        {local ? (
          <div className="flex flex-wrap gap-2">
            {running ? (
              <Button variant="outline" disabled={busy} onClick={() => act(() => api.post("/api/remote/tunnel/stop"))}>
                Stop
              </Button>
            ) : null}
            <Button
              disabled={busy}
              onClick={() =>
                act(async () => {
                  await save();
                  await api.post("/api/remote/tunnel/start");
                })
              }
            >
              {running ? "Save and restart" : "Start"}
            </Button>
            <Button variant="ghost" disabled={busy} onClick={() => act(() => save())}>
              Save
            </Button>
            {data.enabled && (
              <Button variant="ghost" className="ml-auto text-red-600" disabled={busy} onClick={() => act(() => api.put("/api/remote", { enabled: false }))}>
                Turn remote access off
              </Button>
            )}
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">The tunnel is set up from the gateway&apos;s own computer.</p>
        )}
        {!!st.log?.length && (st.state !== "up" || st.error) && (
          <details>
            <summary className="cursor-pointer text-xs text-muted-foreground">cloudflared log</summary>
            <Pre className="mt-2 text-[11px]">{st.log.slice(-20).join("\n")}</Pre>
          </details>
        )}
      </CardContent>
    </Card>
  );
}

function PhoneCard({ data, reload }: { data: RemoteView; reload: () => void }) {
  const [svg, setSvg] = React.useState("");
  React.useEffect(() => {
    if (!data.login_url) return;
    QRCode.toString(data.login_url, { type: "svg", margin: 1, errorCorrectionLevel: "M" })
      .then(setSvg)
      .catch(() => setSvg(""));
  }, [data.login_url]);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Open it on your phone</CardTitle>
        <CardDescription>
          Scan the code with the phone&apos;s camera: it signs that browser in for 30 days. Anyone with this code can use the gateway as you — a new key signs every browser out.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3">
        {data.login_url ? (
          <div className="flex flex-wrap items-start gap-4">
            {/* The SVG comes from the QR library, made here from our own URL. */}
            <div className="size-48 shrink-0 rounded-lg bg-white p-2 [&_svg]:size-full" dangerouslySetInnerHTML={{ __html: svg }} />
            <div className="grid min-w-0 flex-1 gap-2 text-sm">
              <p className="text-muted-foreground">Or send yourself the link:</p>
              <div className="flex items-center gap-2">
                <Mono className="truncate">{data.login_url.replace(/key=.*/, "key=••••")}</Mono>
                <CopyButton text={data.login_url} />
              </div>
              <p className="text-xs text-muted-foreground">Then use Share → Add to Home Screen: it opens as an app.</p>
            </div>
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">Start the tunnel to get a code.</p>
        )}
        {data.key && (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-muted-foreground">Access key</span>
            <Badge variant="outline" className="font-mono">
              {data.key.slice(0, 4)}••••
            </Badge>
            <CopyButton text={data.key} />
            <Button
              variant="outline"
              size="sm"
              className="ml-auto"
              onClick={async () => {
                if (!confirm("Make a new key? Every phone and browser signed in now is signed out.")) return;
                try {
                  await api.post("/api/remote/rotate");
                  toast.success("New key made; every browser is signed out");
                  reload();
                } catch (e) {
                  toast.error((e as Error).message);
                }
              }}
            >
              New key
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function SignedInCard() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Signed in remotely</CardTitle>
        <CardDescription>This browser reaches the gateway through the tunnel. The key, the tunnel&apos;s settings and stopping the gateway stay on its own computer.</CardDescription>
      </CardHeader>
      <CardContent>
        <Button
          variant="outline"
          onClick={async () => {
            await api.post("/api/remote/logout").catch(() => undefined);
            // The sign-in page is the gateway's own, not one of the admin's.
            // eslint-disable-next-line @next/next/no-location-assign-relative-destination
            location.href = "/login";
          }}
        >
          Sign out
        </Button>
      </CardContent>
    </Card>
  );
}
