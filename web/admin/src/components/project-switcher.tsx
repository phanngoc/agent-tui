"use client";

import * as React from "react";
import { CheckIcon, ChevronsUpDownIcon, FolderIcon, FolderOpenIcon, GlobeIcon } from "lucide-react";
import { useFetch } from "@/lib/hooks";
import { useGateway, useVersion } from "@/lib/store";
import type { Project } from "@/lib/types";
import { baseName } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { FolderPicker, describePath } from "@/components/folder-picker";

/**
 * ProjectSwitcher is the project the admin looks at — the scope of the
 * session list, memory, skills, MCP and settings. It used to be a bar across
 * the top of every page; now it is a chip where it is wanted: under the logo,
 * at the head of the session list, in the phone's More sheet.
 */
export function ProjectSwitcher({ variant = "pill", className, onPicked }: { variant?: "pill" | "sidebar" | "row"; className?: string; onPicked?: () => void }) {
  const root = useGateway((s) => s.root);
  const setRoot = useGateway((s) => s.setRoot);
  const v = useVersion("sessions", "peers");
  const [open, setOpen] = React.useState(false);
  const [browsing, setBrowsing] = React.useState(false);
  const [filter, setFilter] = React.useState("");
  const { data: projects } = useFetch<Project[]>(open ? "/api/projects" : null, [v, open]);

  const pick = (r: string) => {
    setRoot(r);
    setOpen(false);
    setFilter("");
    onPicked?.();
  };
  const f = filter.trim().toLowerCase();
  const list = (projects ?? []).filter((p) => p.exists && (!f || p.root.toLowerCase().includes(f)));
  const where = describePath(root);
  const name = root ? baseName(where.where ? where.dir : root) || root : "All projects";
  const Icon = root ? FolderIcon : GlobeIcon;

  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          title={root || "No project picked: everything"}
          className={cn(
            "flex min-w-0 items-center gap-1.5 text-left",
            variant === "pill" && "h-6 max-w-48 rounded px-2 text-xs font-medium hover:bg-muted",
            variant === "sidebar" && "h-8 w-full rounded-lg border bg-background px-2 text-sm hover:bg-muted",
            variant === "row" && "h-11 w-full rounded-xl border px-3 text-sm hover:bg-muted",
            className,
          )}
        >
          <Icon className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate">{name}</span>
          {where.where && <span className="shrink-0 rounded bg-muted px-1 text-[10px] text-muted-foreground">{where.where.replace("WSL ", "wsl ")}</span>}
          <ChevronsUpDownIcon className="size-3 shrink-0 text-muted-foreground" />
        </PopoverTrigger>
        <PopoverContent align="start" className="w-[min(22rem,calc(100vw-2rem))] p-1.5">
          <input
            autoFocus
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && list[0]) pick(list[0].root);
            }}
            placeholder="Find a project"
            className="mb-1 h-9 w-full rounded-md border bg-transparent px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          />
          <div className="max-h-[50dvh] overflow-auto">
            <Row active={!root} onClick={() => pick("")} icon={GlobeIcon} title="All projects" detail="every project's sessions; global skills, MCP, settings" />
            {list.map((p) => {
              const d = describePath(p.root);
              return (
                <Row
                  key={p.root}
                  active={p.root === root}
                  onClick={() => pick(p.root)}
                  icon={FolderIcon}
                  title={p.name}
                  badge={d.where ? d.where.replace("WSL ", "wsl ") : undefined}
                  detail={(d.where ? d.dir : p.root) + (p.peers?.length ? " · open in a terminal" : "")}
                />
              );
            })}
            {!projects && <div className="px-2 py-2 text-xs text-muted-foreground">…</div>}
          </div>
          <button
            type="button"
            onClick={() => {
              setOpen(false);
              setBrowsing(true);
            }}
            className="mt-1 flex w-full items-center gap-2 rounded-md border-t px-2 py-2 text-left text-sm hover:bg-muted"
          >
            <FolderOpenIcon className="size-4 text-muted-foreground" /> Browse folders… <span className="ml-auto text-[11px] text-muted-foreground">WSL too</span>
          </button>
        </PopoverContent>
      </Popover>
      <FolderPicker open={browsing} onOpenChange={setBrowsing} initial={root} onPick={pick} title="Choose the project to look at" />
    </>
  );
}

function Row({ active, onClick, icon: Icon, title, detail, badge }: { active: boolean; onClick: () => void; icon: React.ElementType; title: string; detail?: string; badge?: string }) {
  return (
    <button type="button" onClick={onClick} className={cn("flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left hover:bg-muted", active && "bg-muted")}>
      <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5 text-sm">
          <span className="truncate">{title}</span>
          {badge && <span className="shrink-0 rounded bg-background px-1 text-[10px] text-muted-foreground">{badge}</span>}
        </span>
        {detail && <span className="block truncate font-mono text-[11px] text-muted-foreground">{detail}</span>}
      </span>
      <CheckIcon className={cn("mt-0.5 size-4 shrink-0", active ? "opacity-100" : "opacity-0")} />
    </button>
  );
}
