"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  LayoutDashboardIcon,
  MessagesSquareIcon,
  BrainIcon,
  SparklesIcon,
  PlugIcon,
  SettingsIcon,
  MoonIcon,
  SunIcon,
  FolderIcon,
  GlobeIcon,
  TerminalIcon,
  PanelLeftCloseIcon,
  PanelLeftOpenIcon,
  AlarmClockIcon,
  NetworkIcon,
} from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { onEvent, useGateway, useVersion } from "@/lib/store";
import { useFetch } from "@/lib/hooks";
import type { Project } from "@/lib/types";
import { Dot } from "@/components/common";
import { baseName } from "@/lib/format";
import { Toaster } from "@/components/ui/sonner";
import { FolderPicker } from "@/components/folder-picker";
import { isTyping, useLayout } from "@/lib/layout";

const NAV = [
  { href: "/", label: "Overview", icon: LayoutDashboardIcon },
  { href: "/sessions", label: "Sessions", icon: MessagesSquareIcon },
  { href: "/agents", label: "Agents", icon: NetworkIcon },
  { href: "/memory", label: "Memory", icon: BrainIcon },
  { href: "/skills", label: "Skills", icon: SparklesIcon },
  { href: "/mcp", label: "MCP servers", icon: PlugIcon },
  { href: "/schedules", label: "Schedules", icon: AlarmClockIcon },
  { href: "/settings", label: "Settings", icon: SettingsIcon },
];

export function AppShell({ children }: { children: React.ReactNode }) {
  const connect = useGateway((s) => s.connect);
  React.useEffect(() => connect(), [connect]);
  const pathname = usePathname();
  const connected = useGateway((s) => s.connected);
  const peers = useGateway((s) => s.peers);
  const busy = useGateway((s) => Object.values(s.live).filter((l) => l.busy).length);
  const load = useLayout((s) => s.load);
  const navCollapsed = useLayout((s) => s.navCollapsed);
  const toggleNav = useLayout((s) => s.toggleNav);
  const focus = useLayout((s) => s.focus);
  const setFocus = useLayout((s) => s.setFocus);
  React.useEffect(() => load(), [load]);
  const router = useRouter();
  // A scheduled run with something to say says it, on whatever page is open;
  // a quiet run (HEARTBEAT_OK, NO_REPLY) and a skipped one do not.
  React.useEffect(
    () =>
      onEvent((e) => {
        if (e.type !== "schedule.run") return;
        const run = e.data?.run as { status: string; text?: string; error?: string; session?: string } | undefined;
        if (!run || (run.status !== "ok" && run.status !== "error")) return;
        const body = (run.error || run.text || "").slice(0, 220);
        const opts = {
          description: body,
          duration: 12000,
          action: run.session ? { label: "Open", onClick: () => router.push(`/sessions?id=${run.session}`) } : undefined,
        };
        if (run.status === "error") toast.error(`${e.data?.name} failed`, opts);
        else toast(`${e.data?.name}`, opts);
      }),
    [router],
  );

  // Ctrl/⌘+B folds the navigation, as in an editor; Alt+\ is focus mode.
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "b" && !isTyping(e)) {
        e.preventDefault();
        toggleNav();
      } else if (e.altKey && !e.ctrlKey && !e.metaKey && (e.code === "Backslash" || e.code === "IntlBackslash") && !isTyping(e)) {
        e.preventDefault();
        setFocus(!useLayout.getState().focus);
      } else if (e.key === "Escape" && useLayout.getState().focus && !isTyping(e) && !document.querySelector("[role=dialog]")) {
        setFocus(false);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [toggleNav, setFocus]);

  const rail = navCollapsed || focus;

  return (
    <div className="flex h-dvh min-h-0 w-full overflow-hidden bg-background text-foreground">
      <aside
        className={cn(
          "flex shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground transition-[width] duration-200 ease-out",
          rail ? "w-14" : "w-56",
        )}
      >
        <div className={cn("flex items-center gap-2 py-4", rail ? "justify-center px-2" : "px-4")}>
          <div className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground" title="agent-tui admin">
            <TerminalIcon className="size-4" />
          </div>
          {!rail && (
            <div className="min-w-0 leading-tight">
              <div className="text-sm font-semibold">agent-tui</div>
              <div className="text-[11px] text-muted-foreground">admin · gateway</div>
            </div>
          )}
        </div>
        <nav className="flex flex-col gap-0.5 px-2">
          {NAV.map((n) => {
            const active = n.href === "/" ? pathname === "/" : pathname?.startsWith(n.href);
            const Icon = n.icon;
            return (
              <Link
                key={n.href}
                href={n.href}
                title={rail ? n.label : undefined}
                className={cn(
                  "relative flex items-center gap-2.5 rounded-lg py-1.5 text-sm transition-colors",
                  rail ? "justify-center px-0" : "px-2.5",
                  active ? "bg-sidebar-accent font-medium text-sidebar-accent-foreground" : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground",
                )}
              >
                <Icon className="size-4 shrink-0" />
                {!rail && <span className="flex-1 truncate">{n.label}</span>}
                {n.href === "/sessions" && busy > 0 &&
                  (rail ? (
                    <span className="absolute top-1 right-2 size-2 rounded-full bg-emerald-500" />
                  ) : (
                    <span className="rounded-full bg-emerald-500/15 px-1.5 text-[11px] font-medium text-emerald-600 dark:text-emerald-400">{busy}</span>
                  ))}
              </Link>
            );
          })}
        </nav>
        <div className={cn("mt-auto space-y-2 border-t py-3 text-xs", rail ? "flex flex-col items-center px-2" : "px-4")}>
          <div className="flex items-center gap-2" title={connected ? "Gateway connected" : "Gateway offline"}>
            <Dot on={connected} pulse />
            {!rail && <span className={connected ? "" : "text-muted-foreground"}>{connected ? "Gateway connected" : "Gateway offline"}</span>}
          </div>
          <div
            className="flex items-center gap-2 text-muted-foreground"
            title={peers.length ? peers.map((p) => `${p.id} · ${p.root}`).join("\n") : "no terminal attached"}
          >
            <TerminalIcon className="size-3.5" />
            {!rail && (peers.length === 0 ? "no terminal attached" : `${peers.length} terminal${peers.length > 1 ? "s" : ""} attached`)}
            {rail && peers.length > 0 && <span className="tabular-nums">{peers.length}</span>}
          </div>
          <ThemeToggle compact={rail} />
          <button
            onClick={() => (focus ? setFocus(false) : toggleNav())}
            title={focus ? "Leave focus mode (Alt+\\ or Esc)" : rail ? "Expand the navigation (Ctrl+B)" : "Collapse the navigation (Ctrl+B)"}
            className="flex items-center gap-2 text-muted-foreground hover:text-foreground"
          >
            {rail ? <PanelLeftOpenIcon className="size-3.5" /> : <PanelLeftCloseIcon className="size-3.5" />}
            {!rail && "Collapse"}
          </button>
        </div>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        {!focus && <TopBar />}
        {!connected && <OfflineBanner />}
        <main className="min-h-0 flex-1 overflow-auto">{children}</main>
      </div>
      {focus && (
        <button
          onClick={() => setFocus(false)}
          className="fixed bottom-4 left-1/2 z-40 -translate-x-1/2 rounded-full border bg-background/90 px-3 py-1.5 text-xs text-muted-foreground shadow-md backdrop-blur hover:text-foreground"
        >
          Focus mode · Esc or Alt+\ to bring the panels back
        </button>
      )}
      <Toaster position="bottom-right" />
    </div>
  );
}

function OfflineBanner() {
  return (
    <div className="border-b bg-amber-500/10 px-6 py-2 text-xs text-amber-700 dark:text-amber-300">
      Cannot reach the gateway. Start it with <code className="font-mono">agent-tui serve</code> — opening <code className="font-mono">tui</code> starts one too. This page reconnects on its own.
    </div>
  );
}

function TopBar() {
  const root = useGateway((s) => s.root);
  const setRoot = useGateway((s) => s.setRoot);
  const v = useVersion("sessions", "peers");
  const { data: projects } = useFetch<Project[]>("/api/projects", [v]);
  const [picking, setPicking] = React.useState(false);

  const options = React.useMemo(() => {
    const list = (projects ?? []).filter((p) => p.exists);
    if (root && !list.some((p) => p.root === root)) list.unshift({ root, name: baseName(root), sessions: 0, updated: "", exists: true, config: false });
    return list;
  }, [projects, root]);

  return (
    <header className="flex h-12 shrink-0 items-center gap-3 border-b px-6">
      <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Project</span>
      <div className="flex min-w-0 items-center gap-2">
        {root ? <FolderIcon className="size-4 text-muted-foreground" /> : <GlobeIcon className="size-4 text-muted-foreground" />}
        <select
          value={root}
          onChange={(e) => {
            if (e.target.value === "__custom") setPicking(true);
            else setRoot(e.target.value);
          }}
          className="h-8 max-w-[28rem] truncate rounded-lg border bg-background px-2 text-sm outline-none dark:bg-input/30"
        >
          <option value="">No project — everything</option>
          {options.map((p) => (
            <option key={p.root} value={p.root}>
              {p.name} — {p.root}
              {p.peers?.length ? "  ● open in a terminal" : ""}
            </option>
          ))}
          <option value="__custom">Browse for a folder…</option>
        </select>
        <button className="text-xs text-muted-foreground hover:text-foreground" onClick={() => setPicking(true)} title="Browse folders, WSL included">
          browse…
        </button>
      </div>
      <FolderPicker open={picking} onOpenChange={setPicking} initial={root} onPick={setRoot} title="Choose the project to look at" />
      <span className="ml-auto truncate text-xs text-muted-foreground">
        {root ? "Project scope: settings, skills and MCP in .agent-tui · memory in the data folder" : "Sessions and memory of every project; skills, MCP and settings at global scope"}
      </span>
    </header>
  );
}

function ThemeToggle({ compact }: { compact?: boolean }) {
  // The class on <html> is the state: the label follows it through CSS, so
  // there is nothing to keep in sync and nothing to mismatch on hydration.
  React.useEffect(() => {
    try {
      const saved = localStorage.getItem("agent-tui.theme");
      const dark = saved ? saved === "dark" : window.matchMedia("(prefers-color-scheme: dark)").matches;
      document.documentElement.classList.toggle("dark", dark);
    } catch {}
  }, []);
  return (
    <button
      className="flex items-center gap-2 text-muted-foreground hover:text-foreground"
      onClick={() => {
        const dark = document.documentElement.classList.toggle("dark");
        try {
          localStorage.setItem("agent-tui.theme", dark ? "dark" : "light");
        } catch {}
      }}
    >
      <MoonIcon className="size-3.5 dark:hidden" />
      <SunIcon className="hidden size-3.5 dark:inline" />
      {!compact && (
        <>
          <span className="dark:hidden">Dark theme</span>
          <span className="hidden dark:inline">Light theme</span>
        </>
      )}
    </button>
  );
}
