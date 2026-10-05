"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { PlusIcon, DownloadIcon, PlugZapIcon, Trash2Icon, RefreshCwIcon, CheckCircle2Icon, XCircleIcon } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useGateway, useVersion } from "@/lib/store";
import type { Install, McpServer, McpStatus, Scope } from "@/lib/types";
import { Empty, ErrorNote, Field, Mono, NativeSelect, PageHeader, Pre, ScopeBadge } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import { PaneToggle, Workspace } from "@/components/workspace";

export default function McpPage() {
  return (
    <React.Suspense fallback={null}>
      <Mcp />
    </React.Suspense>
  );
}

interface McpList {
  servers: McpServer[];
  global_path: string;
  project_path: string;
}

function Mcp() {
  const root = useGateway((s) => s.root);
  const params = useSearchParams();
  const router = useRouter();
  const v = useVersion("mcp");
  const { data, error } = useFetch<McpList>("/api/mcp" + qs({ root }), [v, root]);
  const { data: status, loading: checking, reload: recheck } = useFetch<McpStatus[]>("/api/mcp/status" + qs({ root }), [v, root]);
  const [draft, setDraft] = React.useState<McpServer | null>(null);
  const [importing, setImporting] = React.useState(false);
  const want = params.get("name") ?? "";
  const wantScope = params.get("scope") ?? "";
  const servers = data?.servers ?? [];
  const selected = draft ?? servers.find((s) => s.name === want && (!wantScope || s.scope === wantScope) && !s.shadowed) ?? servers.find((s) => s.name === want) ?? null;
  const statusOf = (name: string) => status?.find((s) => s.name === name);

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="MCP servers"
        description={
          <>
            Tools from MCP servers, in Claude Code&apos;s <Mono>mcpServers</Mono> format. The built-in engine connects to them itself (tools appear as{" "}
            <Mono>mcp__server__tool</Mono>); the claude engine is handed the same list.
          </>
        }
        actions={
          <>
            <Button variant="outline" size="sm" onClick={() => recheck()} disabled={checking}>
              <RefreshCwIcon className={cn(checking && "animate-spin")} /> Check all
            </Button>
            <Button variant="outline" size="sm" onClick={() => setImporting(true)}>
              <DownloadIcon /> Import from Claude Code
            </Button>
            <Button size="sm" onClick={() => setDraft({ name: "", type: "stdio", command: "", args: [], scope: root ? "project" : "global" })}>
              <PlusIcon /> Add server
            </Button>
          </>
        }
      />
      <ErrorNote error={error} className="m-6" />
      <div className="min-h-0 flex-1">
        <Workspace
          id="mcp"
          left={{
            node: (
              <div className="h-full overflow-auto p-3">
          {(["project", "global"] as Scope[])
            .filter((sc) => sc === "global" || root)
            .map((sc) => {
              const list = servers.filter((s) => s.scope === sc);
              return (
                <div key={sc} className="mb-5">
                  <div className="mb-1 px-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                    {sc} · {list.length}
                  </div>
                  <div className="mb-2 truncate px-1 font-mono text-[10px] text-muted-foreground">{sc === "global" ? data?.global_path : data?.project_path}</div>
                  {list.length === 0 && <div className="px-1 text-xs text-muted-foreground">none</div>}
                  {list.map((s) => {
                    const st = statusOf(s.name);
                    const inactive = s.shadowed || s.disabled || s.off;
                    return (
                      <button
                        key={s.scope + s.name}
                        onClick={() => {
                          setDraft(null);
                          router.replace(`/mcp?name=${s.name}&scope=${s.scope}`);
                        }}
                        className={cn(
                          "mb-1 w-full rounded-lg border px-2.5 py-2 text-left hover:bg-muted/50",
                          selected?.name === s.name && selected?.scope === s.scope && "border-primary bg-muted",
                          inactive && "opacity-60",
                        )}
                      >
                        <div className="flex items-center gap-1.5">
                          {inactive ? null : st ? (
                            st.connected ? (
                              <CheckCircle2Icon className="size-3.5 text-emerald-600" />
                            ) : (
                              <XCircleIcon className="size-3.5 text-destructive" />
                            )
                          ) : (
                            <span className="size-3.5 animate-pulse rounded-full bg-muted" />
                          )}
                          <span className="truncate text-sm font-medium">{s.name}</span>
                          <Badge variant="outline" className="ml-auto text-[10px] font-normal">
                            {s.type || (s.command ? "stdio" : "http")}
                          </Badge>
                        </div>
                        <div className="truncate font-mono text-[11px] text-muted-foreground">{s.command ? [s.command, ...(s.args ?? [])].join(" ") : s.url}</div>
                        <div className="text-[11px] text-muted-foreground">
                          {s.shadowed ? "replaced by the project's" : s.disabled ? "disabled" : s.off ? "off in this project" : st?.connected ? `${st.tools.length} tools` : st?.error ? "failed" : ""}
                        </div>
                      </button>
                    );
                  })}
                </div>
              );
            })}
              </div>
            ),
            defaultSize: 320,
            minSize: 220,
            maxSize: 560,
            foldBelow: 820,
            label: "server list",
          }}
        >
          <div className="min-h-0 flex-1 overflow-auto p-6">
            <PaneToggle side="left" className="-mt-3 -ml-3 mb-1" />
          {selected ? (
            <ServerEditor
              key={selected.scope + selected.name + (draft ? "d" : "")}
              server={selected}
              isNew={!!draft}
              root={root}
              status={statusOf(selected.name)}
              onDone={(s) => {
                setDraft(null);
                router.replace(s ? `/mcp?name=${s.name}&scope=${s.scope}` : "/mcp");
              }}
            />
          ) : (
            <Empty title="Pick a server">Status is checked by connecting to each enabled server and listing its tools — exactly what a turn would do.</Empty>
          )}
          </div>
        </Workspace>
      </div>
      <ImportDialog open={importing} onOpenChange={setImporting} root={root} />
    </div>
  );
}

const lines = (s: string) =>
  s
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);

function toPairs(m: Record<string, string> | undefined, sep: string) {
  return Object.entries(m ?? {})
    .map(([k, v]) => `${k}${sep}${v}`)
    .join("\n");
}

function fromPairs(s: string, sep: string): Record<string, string> | undefined {
  const out: Record<string, string> = {};
  for (const l of lines(s)) {
    const i = l.indexOf(sep);
    if (i > 0) out[l.slice(0, i).trim()] = l.slice(i + sep.length).trim();
  }
  return Object.keys(out).length ? out : undefined;
}

function ServerEditor({ server, isNew, root, status, onDone }: { server: McpServer; isNew: boolean; root: string; status?: McpStatus; onDone: (s?: McpServer) => void }) {
  const transport = server.type || (server.command ? "stdio" : "http");
  const [name, setName] = React.useState(server.name);
  const [scope, setScope] = React.useState<Scope>(server.scope);
  const [type, setType] = React.useState(transport);
  const [command, setCommand] = React.useState(server.command ?? "");
  const [args, setArgs] = React.useState((server.args ?? []).join("\n"));
  const [env, setEnv] = React.useState(toPairs(server.env, "="));
  const [url, setUrl] = React.useState(server.url ?? "");
  const [headers, setHeaders] = React.useState(toPairs(server.headers, ": "));
  const [disabled, setDisabled] = React.useState(!!server.disabled);
  const [err, setErr] = React.useState<string | null>(null);
  const [test, setTest] = React.useState<McpStatus | null>(null);
  const [testing, setTesting] = React.useState(false);

  const def = (): McpServer => ({
    name,
    scope,
    type,
    disabled,
    ...(type === "stdio" ? { command, args: lines(args), env: fromPairs(env, "=") } : { url, headers: fromPairs(headers, ":") }),
  });
  const shown = test ?? status;

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-lg font-semibold">{isNew ? "Add server" : server.name}</h2>
        {!isNew && <ScopeBadge scope={server.scope} />}
        {!isNew && root && (
          <label className="ml-auto flex items-center gap-2 text-sm">
            <Switch
              checked={!server.off}
              onCheckedChange={async (on) => {
                try {
                  await api.post("/api/project/toggle", { root, kind: "mcp", name: server.name, enabled: on });
                } catch (e) {
                  toast.error((e as Error).message);
                }
              }}
            />
            on in this project
          </label>
        )}
      </div>
      <div className="grid gap-3 md:grid-cols-3">
        <Field label="Name">
          <Input value={name} onChange={(e) => setName(e.target.value)} className="font-mono" />
        </Field>
        <Field label="Scope">
          <NativeSelect value={scope} onChange={(v) => setScope(v as Scope)} options={[...(root ? [{ value: "project", label: "project" }] : []), { value: "global", label: "global" }]} />
        </Field>
        <Field label="Transport">
          <NativeSelect
            value={type}
            onChange={setType}
            options={[
              { value: "stdio", label: "stdio (a command)" },
              { value: "http", label: "streamable HTTP" },
              { value: "sse", label: "SSE (legacy)" },
            ]}
          />
        </Field>
      </div>
      {type === "stdio" ? (
        <>
          <Field label="Command">
            <Input value={command} onChange={(e) => setCommand(e.target.value)} className="font-mono" placeholder="npx" />
          </Field>
          <div className="grid gap-3 md:grid-cols-2">
            <Field label="Arguments" hint="one per line">
              <Textarea rows={5} value={args} onChange={(e) => setArgs(e.target.value)} className="font-mono text-xs" placeholder={"-y\n@modelcontextprotocol/server-filesystem\nC:\\code"} />
            </Field>
            <Field label="Environment" hint="KEY=value, one per line">
              <Textarea rows={5} value={env} onChange={(e) => setEnv(e.target.value)} className="font-mono text-xs" />
            </Field>
          </div>
        </>
      ) : (
        <>
          <Field label="URL">
            <Input value={url} onChange={(e) => setUrl(e.target.value)} className="font-mono" placeholder="https://example.com/mcp" />
          </Field>
          <Field label="Headers" hint="Name: value, one per line. $VARS are expanded from the gateway's environment.">
            <Textarea rows={3} value={headers} onChange={(e) => setHeaders(e.target.value)} className="font-mono text-xs" placeholder="Authorization: Bearer $MY_TOKEN" />
          </Field>
        </>
      )}
      <label className="flex items-center gap-2 text-sm">
        <Switch checked={disabled} onCheckedChange={setDisabled} /> Disabled everywhere (keep the definition)
      </label>
      <ErrorNote error={err} />
      <div className="flex flex-wrap gap-2">
        <Button
          onClick={async () => {
            setErr(null);
            try {
              const saved = await api.put<McpServer>("/api/mcp", { root, old_name: isNew ? "" : server.name, old_scope: isNew ? "" : server.scope, server: def() });
              toast.success("Saved");
              onDone(saved);
            } catch (e) {
              setErr((e as Error).message);
            }
          }}
        >
          Save
        </Button>
        <Button
          variant="outline"
          disabled={testing}
          onClick={async () => {
            setTesting(true);
            setErr(null);
            try {
              setTest(await api.post<McpStatus>("/api/mcp/test", def()));
            } catch (e) {
              setErr((e as Error).message);
            } finally {
              setTesting(false);
            }
          }}
        >
          <PlugZapIcon /> {testing ? "Connecting…" : "Test connection"}
        </Button>
        {!isNew && (
          <Button
            variant="destructive"
            onClick={async () => {
              if (!confirm(`Remove ${server.name} from the ${server.scope} servers?`)) return;
              try {
                await api.del("/api/mcp" + qs({ root, scope: server.scope, name: server.name }));
                onDone();
              } catch (e) {
                toast.error((e as Error).message);
              }
            }}
          >
            <Trash2Icon /> Delete
          </Button>
        )}
      </div>

      {shown && (
        <div className="space-y-2 rounded-xl border p-4">
          <div className="flex items-center gap-2 text-sm font-medium">
            {shown.connected ? <CheckCircle2Icon className="size-4 text-emerald-600" /> : <XCircleIcon className="size-4 text-destructive" />}
            {shown.connected ? `Connected${shown.server_info ? ` to ${shown.server_info}` : ""} — ${shown.tools.length} tools` : "Could not connect"}
            {test && <Badge variant="outline">test of the form above</Badge>}
          </div>
          {shown.error && <Pre max="max-h-40">{shown.error}</Pre>}
          <div className="space-y-1.5">
            {shown.tools.map((t) => (
              <details key={t.name} className="rounded-lg border px-3 py-2">
                <summary className="cursor-pointer text-sm">
                  <span className="font-mono">{t.name}</span> <span className="text-xs text-muted-foreground">{t.description?.split("\n")[0]}</span>
                </summary>
                <Pre max="max-h-60" className="mt-2">
                  {JSON.stringify(t.inputSchema ?? {}, null, 2)}
                </Pre>
              </details>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function ImportDialog({ open, onOpenChange, root }: { open: boolean; onOpenChange: (o: boolean) => void; root: string }) {
  const { data, loading, error } = useFetch<Install[]>(open ? "/api/claude/installs" : null, [open]);
  const [scope, setScope] = React.useState<Scope>(root ? "project" : "global");
  return (
    <Dialog open={open} onOpenChange={(o) => onOpenChange(o)}>
      <DialogContent className="max-h-[85vh] overflow-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Import MCP servers from Claude Code</DialogTitle>
          <DialogDescription>
            From <Mono>~/.claude.json</Mono> — user-level and per-project servers — on this machine and in each WSL distribution. A WSL server&apos;s command runs on
            Windows once imported; edit it if it needs <Mono>wsl.exe</Mono> in front.
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2 text-sm">
          Import into
          <NativeSelect value={scope} onChange={(v) => setScope(v as Scope)} options={[...(root ? [{ value: "project", label: "this project" }] : []), { value: "global", label: "global" }]} />
        </div>
        {loading && !data && <div className="text-sm text-muted-foreground">Looking for installs…</div>}
        <ErrorNote error={error} />
        {(data ?? []).map((inst) => (
          <div key={inst.id} className="space-y-2">
            <div className="text-sm font-medium">{inst.label}</div>
            {inst.servers.length === 0 && <div className="text-xs text-muted-foreground">no servers</div>}
            {inst.servers.map((s) => (
              <div key={(s.project ?? "") + s.name} className="flex items-start gap-3 rounded-lg border p-2.5">
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium">
                    {s.name} <span className="text-xs font-normal text-muted-foreground">{s.type || (s.command ? "stdio" : "http")}</span>
                  </div>
                  <div className="truncate font-mono text-[11px] text-muted-foreground">{s.command ? [s.command, ...(s.args ?? [])].join(" ") : s.url}</div>
                  <div className="truncate text-[11px] text-muted-foreground">{s.project ? `project ${s.project}` : "user-level"}</div>
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={async () => {
                    try {
                      await api.post("/api/mcp/import", { install: inst.id, name: s.name, project: s.project ?? "", scope, root });
                      toast.success(`Imported ${s.name}`);
                    } catch (e) {
                      toast.error((e as Error).message);
                    }
                  }}
                >
                  Import
                </Button>
              </div>
            ))}
          </div>
        ))}
      </DialogContent>
    </Dialog>
  );
}
