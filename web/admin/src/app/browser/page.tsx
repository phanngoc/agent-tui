"use client";

import * as React from "react";
import { CameraIcon, CheckIcon, AppWindowIcon, FolderOpenIcon, GlobeIcon, ScanTextIcon, XIcon } from "lucide-react";
import { toast } from "sonner";
import { api, gatewayBase } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { Ago, CopyButton, Dot, ErrorNote, Mono, PageHeader, Pre } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

type Client = { id: string; name: string; version: string; approved: string; last_seen: string };
type Pending = { id: string; name: string; version: string; code: string; at: string };
type Status = { connected: boolean; clients: Client[]; pending: Pending[]; extension_dir: string; extension_written: boolean };

/**
 * The Browser page: the user's Chrome, driven by the agent through the
 * agent-tui extension. Set it up, approve it, and try it.
 */
export default function BrowserPage() {
  const [tick, setTick] = React.useState(0);
  const { data: st, error, reload } = useFetch<Status>("/api/browser/status", [tick]);
  // Waiting for an extension to call or be approved: keep looking.
  React.useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 3000);
    return () => clearInterval(t);
  }, []);
  const [dir, setDir] = React.useState<string | null>(null);

  const act = async (path: string, body: unknown, ok: string) => {
    try {
      await api.post(path, body);
      toast.success(ok);
      reload();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  return (
    <div>
      <PageHeader
        title="Browser"
        description="Let the agent work in your own Chrome — pages behind your logins, web apps to operate — through the agent-tui extension. It only touches the tabs it opens (an “agent-tui” tab group) and the ones you share from the extension’s popup."
      />
      <div className="max-w-4xl space-y-6 p-6">
        <ErrorNote error={error} />
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Dot on={!!st?.connected} pulse={false} />
              {st?.connected ? "Chrome is connected" : st?.pending.length ? "An extension is asking to connect" : "Chrome is not connected"}
            </CardTitle>
            <CardDescription>
              {st?.connected
                ? "The agent has browser tools on its next turn: browser_open, browser_snapshot, browser_click, browser_type, browser_screenshot…"
                : "Set up the extension below; once approved, the agent gets browser tools on its next turn."}
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {st?.pending.map((p) => (
              <div key={p.id} className="flex flex-wrap items-center gap-3 rounded-lg border border-amber-500/40 bg-amber-500/5 p-3">
                <div className="font-mono text-2xl font-semibold tracking-[0.3em]">{p.code}</div>
                <div className="min-w-0 flex-1 text-sm">
                  <div>
                    {p.name || "Chrome"} · extension {p.version}
                  </div>
                  <div className="text-xs text-muted-foreground">
                    Approve only if the extension&apos;s popup shows this same code. Asked <Ago at={p.at} />.
                  </div>
                </div>
                <Button size="sm" onClick={() => act("/api/browser/approve", { id: p.id }, "Approved: the extension connects in a moment")}>
                  <CheckIcon /> Approve
                </Button>
                <Button size="sm" variant="ghost" onClick={() => act("/api/browser/reject", { id: p.id }, "Rejected")}>
                  <XIcon /> Reject
                </Button>
              </div>
            ))}
            {st?.clients.map((c) => (
              <div key={c.id} className="flex items-center gap-3 rounded-lg border p-3 text-sm">
                <AppWindowIcon className="size-4 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div>
                    {c.name || "Chrome"} · extension {c.version}
                  </div>
                  <div className="text-xs text-muted-foreground">
                    approved <Ago at={c.approved} /> · last seen {c.last_seen && !c.last_seen.startsWith("0001") ? <Ago at={c.last_seen} /> : "never"}
                  </div>
                </div>
                <Button size="sm" variant="ghost" onClick={() => confirm("Revoke this extension? It stops working until approved again.") && act("/api/browser/revoke", { id: c.id }, "Revoked")}>
                  Revoke
                </Button>
              </div>
            ))}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Set up the extension</CardTitle>
            <CardDescription>Once, in the Chrome you want the agent to use.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4 text-sm">
            <Step n={1} title="Get the extension’s folder">
              <Button
                size="sm"
                variant="outline"
                onClick={async () => {
                  try {
                    const r = await api.post<{ path: string }>("/api/browser/extension", {});
                    setDir(r.path);
                    toast.success("Extension folder ready");
                    reload();
                  } catch (e) {
                    toast.error((e as Error).message);
                  }
                }}
              >
                <FolderOpenIcon /> {st?.extension_written ? "Write it again (after updating agent-tui)" : "Write the extension folder"}
              </Button>
              {(dir || st?.extension_written) && (
                <div className="mt-2 flex items-center gap-2">
                  <Mono className="break-all">{dir ?? st?.extension_dir}</Mono>
                  <CopyButton text={dir ?? st?.extension_dir ?? ""} />
                </div>
              )}
              <p className="mt-1 text-xs text-muted-foreground">
                It is set to this gateway (<Mono>{gatewayBase() || "this address"}</Mono>). After updating agent-tui, write it again and press reload on the extension.
              </p>
            </Step>
            <Step n={2} title="Load it in Chrome">
              Open <Mono>chrome://extensions</Mono> <CopyButton text="chrome://extensions" />, turn on <b>Developer mode</b>, click <b>Load unpacked</b> and pick the folder above.
            </Step>
            <Step n={3} title="Approve it here">
              The extension asks to connect with a 4-digit code, shown in its popup and above. Approve it when they match.
            </Step>
          </CardContent>
        </Card>

        <TryIt connected={!!st?.connected} />
      </div>
    </div>
  );
}

function Step({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <div className="flex gap-3">
      <div className="grid size-6 shrink-0 place-items-center rounded-full border text-xs font-medium">{n}</div>
      <div className="min-w-0 flex-1">
        <div className="mb-1 font-medium">{title}</div>
        <div className="text-muted-foreground">{children}</div>
      </div>
    </div>
  );
}

/** TryIt drives the browser by hand, the way the agent's tools do. */
function TryIt({ connected }: { connected: boolean }) {
  const [url, setUrl] = React.useState("https://example.com");
  const [busy, setBusy] = React.useState(false);
  const [out, setOut] = React.useState<string>("");
  const [shot, setShot] = React.useState<string | null>(null);
  const run = async (action: string, args: Record<string, unknown> = {}) => {
    setBusy(true);
    try {
      const r = await api.post<Record<string, unknown>>("/api/browser/do", { action, args });
      if (action === "screenshot" && typeof r.name === "string") setShot(`${gatewayBase()}/api/browser/shot?name=${encodeURIComponent(r.name)}`);
      if (action === "snapshot") setOut(`${r.title}\n${r.url}\n\n${r.elements}\n\n${String(r.text ?? "").slice(0, 3000)}`);
      else setOut(JSON.stringify(r, null, 2));
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Try it</CardTitle>
        <CardDescription>What the agent&apos;s tools do, by hand.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            void run("open", { url });
          }}
        >
          <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://…" className="font-mono text-xs" />
          <Button type="submit" size="sm" disabled={!connected || busy}>
            <GlobeIcon /> Open
          </Button>
        </form>
        <div className="flex gap-2">
          <Button size="sm" variant="outline" disabled={!connected || busy} onClick={() => void run("snapshot")}>
            <ScanTextIcon /> Snapshot
          </Button>
          <Button size="sm" variant="outline" disabled={!connected || busy} onClick={() => void run("screenshot")}>
            <CameraIcon /> Screenshot
          </Button>
          <Button size="sm" variant="ghost" disabled={!connected || busy} onClick={() => void run("tabs")}>
            Tabs
          </Button>
        </div>
        {!connected && <p className="text-xs text-muted-foreground">Connect Chrome first.</p>}
        {/* eslint-disable-next-line @next/next/no-img-element */}
        {shot && <img src={shot} alt="Screenshot of the tab" className="max-h-96 rounded-lg border" />}
        {out && <Pre max="max-h-96">{out}</Pre>}
      </CardContent>
    </Card>
  );
}
