"use client";

import * as React from "react";
import { PlusIcon, XIcon, SquareTerminalIcon } from "lucide-react";
import { toast } from "sonner";
import { api, gatewayBase, qs } from "@/lib/api";
import { cn } from "@/lib/utils";
import "@xterm/xterm/css/xterm.css";

// The project's terminals: real shells in its folder — the distribution's for
// a project in WSL — run by the gateway and drawn here with xterm.js. They
// belong to the gateway, so a reload or another tab reattaches to them, with
// what they printed lately replayed.

type TermInfo = { id: string; shell: string; exited?: boolean };
type Shell = { id: string; label: string };

function decode(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

const darkTheme = { background: "#0a0a0a", foreground: "#e5e5e5", cursor: "#e5e5e5", selectionBackground: "#3b82f680" };
const lightTheme = { background: "#ffffff", foreground: "#171717", cursor: "#171717", selectionBackground: "#3b82f640" };

/** TermView draws one terminal and wires it to the gateway. */
function TermView({ id, active }: { id: string; active: boolean }) {
  const box = React.useRef<HTMLDivElement>(null);
  const fitRef = React.useRef<(() => void) | null>(null);
  React.useEffect(() => {
    let disposed = false;
    let cleanup = () => {};
    (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([import("@xterm/xterm"), import("@xterm/addon-fit")]);
      if (disposed || !box.current) return;
      const dark = document.documentElement.classList.contains("dark");
      const term = new Terminal({
        fontFamily: "'Cascadia Mono', 'Cascadia Code', Consolas, 'Geist Mono', monospace",
        fontSize: 13,
        cursorBlink: true,
        scrollback: 5000,
        theme: dark ? darkTheme : lightTheme,
        allowProposedApi: false,
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(box.current);

      // Input in order: each send waits for the one before.
      let chain = Promise.resolve();
      let pending = "";
      let timer: ReturnType<typeof setTimeout> | null = null;
      const flush = () => {
        timer = null;
        const data = pending;
        pending = "";
        if (!data) return;
        chain = chain.then(() => api.post(`/api/term/${id}/input`, { data }).then(() => undefined)).catch(() => undefined);
      };
      const onData = term.onData((d) => {
        pending += d;
        if (!timer) timer = setTimeout(flush, 8);
      });

      let lastSize = "";
      const resize = () => {
        try {
          fit.fit();
        } catch {
          return;
        }
        const size = `${term.cols}x${term.rows}`;
        if (size === lastSize) return;
        lastSize = size;
        void api.post(`/api/term/${id}/resize`, { cols: term.cols, rows: term.rows }).catch(() => undefined);
      };
      fitRef.current = resize;
      const ro = new ResizeObserver(() => resize());
      ro.observe(box.current);
      resize();

      const es = new EventSource(gatewayBase() + `/api/term/${id}/stream`);
      es.addEventListener("data", (e) => term.write(decode((e as MessageEvent).data)));
      es.addEventListener("exit", (e) => {
        term.write(`\r\n\x1b[2m[process exited with code ${(e as MessageEvent).data}]\x1b[0m\r\n`);
        es.close();
      });

      const themeObs = new MutationObserver(() => {
        term.options.theme = document.documentElement.classList.contains("dark") ? darkTheme : lightTheme;
      });
      themeObs.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });

      if (active) term.focus();
      cleanup = () => {
        es.close();
        ro.disconnect();
        themeObs.disconnect();
        onData.dispose();
        term.dispose();
      };
    })();
    return () => {
      disposed = true;
      cleanup();
    };
    // `active` only sets the first focus.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);
  React.useEffect(() => {
    if (active) requestAnimationFrame(() => fitRef.current?.());
  }, [active]);
  return <div ref={box} className={cn("absolute inset-0 px-2 py-1", !active && "invisible")} />;
}

/** TerminalPanel is the project's terminals, as tabs, with a way to open one. */
export function TerminalPanel({ root, onClose }: { root: string; onClose: () => void }) {
  const [terms, setTerms] = React.useState<TermInfo[] | null>(null);
  const [active, setActive] = React.useState<string>("");
  const [shells, setShells] = React.useState<Shell[]>([]);

  const open = React.useCallback(
    async (shell?: string) => {
      try {
        const t = await api.post<{ id: string; shell: string }>("/api/term", { root, shell, cols: 100, rows: 24 });
        setTerms((ts) => [...(ts ?? []), { id: t.id, shell: t.shell }]);
        setActive(t.id);
      } catch (e) {
        toast.error((e as Error).message);
      }
    },
    [root],
  );

  // Reattach to the project's terminals, or open the first one.
  React.useEffect(() => {
    let live = true;
    Promise.all([api.get<{ terms: TermInfo[] }>("/api/term" + qs({ root })), api.get<{ shells: Shell[] }>("/api/term/shells" + qs({ root }))])
      .then(([t, s]) => {
        if (!live) return;
        setShells(s.shells);
        const alive = t.terms.filter((x) => !x.exited);
        setTerms(alive);
        if (alive.length) setActive(alive[alive.length - 1].id);
        else void open();
      })
      .catch((e) => toast.error((e as Error).message));
    return () => {
      live = false;
    };
  }, [root, open]);

  const close = async (id: string) => {
    await api.del(`/api/term/${id}`).catch(() => undefined);
    setTerms((ts) => {
      const rest = (ts ?? []).filter((t) => t.id !== id);
      if (active === id) setActive(rest[rest.length - 1]?.id ?? "");
      if (rest.length === 0) onClose();
      return rest;
    });
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex items-center gap-1 border-b px-2 py-1 text-xs">
        <SquareTerminalIcon className="mr-1 size-3.5 text-muted-foreground" />
        <div className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
          {(terms ?? []).map((t, i) => (
            <div
              key={t.id}
              className={cn("group flex shrink-0 items-center gap-1 rounded-md border px-2 py-0.5", active === t.id ? "border-primary/50 bg-muted" : "border-transparent hover:bg-muted/60")}
            >
              <button onClick={() => setActive(t.id)} className="max-w-40 truncate">
                {i + 1}: {t.shell}
              </button>
              <button onClick={() => close(t.id)} className="rounded opacity-50 hover:bg-background hover:opacity-100" title="Close this terminal (ends the shell)">
                <XIcon className="size-3" />
              </button>
            </div>
          ))}
          <select
            value=""
            onChange={(e) => e.target.value && open(e.target.value)}
            className="rounded-md border-0 bg-transparent px-1 py-0.5 text-muted-foreground hover:bg-muted"
            title="Open another terminal"
            aria-label="Open another terminal"
          >
            <option value="">＋ new</option>
            {shells.map((s) => (
              <option key={s.id} value={s.id}>
                {s.label}
              </option>
            ))}
          </select>
          <button onClick={() => open()} className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground" title="New terminal">
            <PlusIcon className="size-3.5" />
          </button>
        </div>
        <button onClick={onClose} className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground" title="Hide the panel (the shells keep running)">
          <XIcon className="size-3.5" />
        </button>
      </div>
      <div className="relative min-h-0 flex-1">
        {(terms ?? []).map((t) => (
          <TermView key={t.id} id={t.id} active={active === t.id} />
        ))}
        {terms && terms.length === 0 && <div className="p-3 text-xs text-muted-foreground">Opening a terminal…</div>}
      </div>
    </div>
  );
}
