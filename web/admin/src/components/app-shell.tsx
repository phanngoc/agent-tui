"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  LayoutDashboardIcon,
  MessagesSquareIcon,
  BrainIcon,
  LibraryIcon,
  SquareKanbanIcon,
  SparklesIcon,
  PlugIcon,
  SettingsIcon,
  MoonIcon,
  SunIcon,
  TerminalIcon,
  PanelLeftCloseIcon,
  PanelLeftOpenIcon,
  AlarmClockIcon,
  NetworkIcon,
  CodeXmlIcon,
  RadioTowerIcon,
  EllipsisIcon,
  CoffeeIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useGateway, useVersion } from "@/lib/store";
import { useFetch } from "@/lib/hooks";
import type { AwakeStatus } from "@/components/keep-awake-card";
import { Dot } from "@/components/common";
import { Toaster } from "@/components/ui/sonner";
import { ProjectSwitcher } from "@/components/project-switcher";
import { isTyping, useLayout } from "@/lib/layout";
import { useIsMobile } from "@/lib/mobile";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";

const NAV = [
  { href: "/", label: "Overview", icon: LayoutDashboardIcon },
  { href: "/sessions", label: "Sessions", icon: MessagesSquareIcon },
  { href: "/board", label: "Board", icon: SquareKanbanIcon },
  { href: "/agents", label: "Agents", icon: NetworkIcon },
  { href: "/editor", label: "Editor", icon: CodeXmlIcon },
  { href: "/memory", label: "Memory", icon: BrainIcon },
  { href: "/wiki", label: "Wiki", icon: LibraryIcon },
  { href: "/skills", label: "Skills", icon: SparklesIcon },
  { href: "/mcp", label: "MCP servers", icon: PlugIcon },
  { href: "/schedules", label: "Schedules", icon: AlarmClockIcon },
  { href: "/remote", label: "Remote & Telegram", icon: RadioTowerIcon },
  { href: "/settings", label: "Settings", icon: SettingsIcon },
];

// On a phone, the pages at the bottom, under the thumb; the rest behind More.
const MOBILE_TABS = ["/", "/sessions", "/agents", "/schedules"];

const isActive = (href: string, pathname: string | null) => (href === "/" ? pathname === "/" : !!pathname?.startsWith(href));

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
  // A scheduled run's report is in its session, which the list marks as
  // updated; it no longer pops up over whatever page is open.

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
  const mobile = useIsMobile();

  return (
    <div className="flex h-dvh min-h-0 w-full overflow-hidden bg-background pt-[env(safe-area-inset-top)] text-foreground">
      <aside
        className={cn(
          "hidden shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground transition-[width] duration-200 ease-out md:flex",
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
        {/* The project in view: once a bar across the top of every page. */}
        {!rail && (
          <div className="px-2 pb-3">
            <ProjectSwitcher variant="sidebar" />
          </div>
        )}
        <nav className="flex flex-col gap-0.5 px-2">
          {NAV.map((n) => {
            const active = isActive(n.href, pathname);
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
          <AwakeLine compact={rail} />
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
        {!connected && <OfflineBanner />}
        <main className="min-h-0 flex-1 overflow-auto">{children}</main>
        <MobileNav busy={busy} connected={connected} />
      </div>
      {focus && (
        <button
          onClick={() => setFocus(false)}
          className="fixed bottom-4 left-1/2 z-40 -translate-x-1/2 rounded-full border bg-background/90 px-3 py-1.5 text-xs text-muted-foreground shadow-md backdrop-blur hover:text-foreground"
        >
          Focus mode · Esc or Alt+\ to bring the panels back
        </button>
      )}
      <Toaster position={mobile ? "top-center" : "bottom-right"} />
    </div>
  );
}

/** MobileNav is the bottom tab bar on a phone, with the rest of the pages behind More. */
function MobileNav({ busy, connected }: { busy: number; connected: boolean }) {
  const pathname = usePathname();
  const [more, setMore] = React.useState(false);
  const tabs = NAV.filter((n) => MOBILE_TABS.includes(n.href));
  const rest = NAV.filter((n) => !MOBILE_TABS.includes(n.href));
  const moreActive = rest.some((n) => isActive(n.href, pathname));
  const tab = "relative flex min-w-0 flex-1 flex-col items-center justify-center gap-0.5 py-1.5 text-[11px]";
  return (
    <nav className="flex shrink-0 border-t bg-sidebar pb-[env(safe-area-inset-bottom)] md:hidden">
      {tabs.map((n) => {
        const Icon = n.icon;
        const active = isActive(n.href, pathname);
        return (
          <Link key={n.href} href={n.href} className={cn(tab, active ? "font-medium text-foreground" : "text-muted-foreground")}>
            <Icon className="size-5" />
            <span className="truncate">{n.label}</span>
            {n.href === "/sessions" && busy > 0 && (
              <span className="absolute top-1 left-1/2 ml-2 rounded-full bg-emerald-500 px-1 text-[10px] leading-4 font-medium text-white">{busy}</span>
            )}
          </Link>
        );
      })}
      <button type="button" onClick={() => setMore(true)} className={cn(tab, moreActive ? "font-medium text-foreground" : "text-muted-foreground")}>
        <EllipsisIcon className="size-5" />
        <span>More</span>
        {!connected && <span className="absolute top-1.5 left-1/2 ml-2 size-2 rounded-full bg-amber-500" />}
      </button>
      <Sheet open={more} onOpenChange={setMore}>
        <SheetContent side="bottom" className="max-h-[85dvh] rounded-t-2xl pb-[calc(env(safe-area-inset-bottom)+1rem)]">
          <SheetTitle className="px-5 pt-4 text-base">agent-tui</SheetTitle>
          <div className="px-4">
            <ProjectSwitcher variant="row" onPicked={() => setMore(false)} />
          </div>
          <div className="grid grid-cols-3 gap-2 px-4">
            {rest.map((n) => {
              const Icon = n.icon;
              return (
                <Link
                  key={n.href}
                  href={n.href}
                  onClick={() => setMore(false)}
                  className={cn(
                    "flex flex-col items-center gap-1.5 rounded-xl border px-2 py-3 text-center text-xs",
                    isActive(n.href, pathname) ? "border-primary bg-muted font-medium" : "text-muted-foreground",
                  )}
                >
                  <Icon className="size-5" />
                  {n.label}
                </Link>
              );
            })}
          </div>
          <div className="flex items-center gap-4 border-t px-5 pt-3 text-xs">
            <span className="flex items-center gap-2">
              <Dot on={connected} pulse />
              {connected ? "Gateway connected" : "Gateway offline"}
            </span>
            <span className="ml-auto">
              <ThemeToggle />
            </span>
          </div>
        </SheetContent>
      </Sheet>
    </nav>
  );
}

function OfflineBanner() {
  return (
    <div className="border-b bg-amber-500/10 px-3 py-2 text-xs text-amber-700 md:px-6 dark:text-amber-300">
      Cannot reach the gateway. Start it with <code className="font-mono">agent-tui serve</code> — opening <code className="font-mono">tui</code> starts one too. This page reconnects on its own.
    </div>
  );
}

/** AwakeLine says, in the sidebar, that the computer is being kept awake. */
function AwakeLine({ compact }: { compact?: boolean }) {
  const v = useVersion("settings", "sessions");
  const { data, reload } = useFetch<AwakeStatus>("/api/awake", [v]);
  React.useEffect(() => {
    const t = setInterval(reload, 15000);
    return () => clearInterval(t);
  }, [reload]);
  if (!data?.active) return null;
  return (
    <Link href="/settings" title={"Keeping the computer awake: " + (data.reason ?? "")} className="flex items-center gap-2 text-amber-600 hover:text-amber-700 dark:text-amber-400">
      <CoffeeIcon className="size-3.5" />
      {!compact && <span className="truncate">awake · {data.reason}</span>}
    </Link>
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
