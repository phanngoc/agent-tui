"use client";

import * as React from "react";
import { ArrowUpIcon, ClockIcon, FolderIcon, FolderGit2Icon, HardDriveIcon, SearchIcon, TerminalSquareIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { ErrorNote } from "@/components/common";

interface Place {
  label: string;
  path: string;
  target?: string;
  cwd?: string;
  detail?: string;
}

interface Roots {
  home: string;
  drives: Place[];
  wsl: Place[];
  recent: Place[];
}

interface Listing {
  path: string;
  parent: string;
  entries: { name: string; path: string; project?: boolean; hidden?: boolean }[];
  wsl?: { distro: string; linux: string; target: string };
}

/** wslDisplay turns a place inside a distribution into its Windows path. */
export function placePath(p: { path?: string; root?: string; target?: string; cwd?: string }): string {
  const base = p.path ?? p.root ?? "";
  if (p.target?.startsWith("wsl:") && p.cwd) {
    return `\\\\wsl.localhost\\${p.target.slice(4)}${p.cwd.replace(/\//g, "\\")}`;
  }
  return base;
}

/** describe says where a path is, for a label: a WSL distribution or this machine. */
export function describePath(path: string): { where: string; dir: string } {
  const m = /^[\\/]{2}wsl(?:\.localhost|\$)[\\/]([^\\/]+)(.*)$/i.exec(path);
  if (m) return { where: `WSL ${m[1]}`, dir: (m[2] || "/").replace(/\\/g, "/") };
  return { where: "", dir: path };
}

/** crumbs splits a path into clickable steps, keeping a drive or a share as one. */
function crumbs(path: string): { label: string; path: string }[] {
  const wsl = /^([\\/]{2}wsl(?:\.localhost|\$)[\\/][^\\/]+)(.*)$/i.exec(path);
  let root: string;
  let rest: string;
  let rootLabel: string;
  if (wsl) {
    root = wsl[1].replace(/\//g, "\\");
    rest = wsl[2];
    rootLabel = "WSL " + root.split("\\").pop();
  } else if (/^[A-Za-z]:/.test(path)) {
    root = path.slice(0, 2) + "\\";
    rest = path.slice(2);
    rootLabel = path.slice(0, 2);
  } else {
    root = "/";
    rest = path;
    rootLabel = "/";
  }
  const out = [{ label: rootLabel, path: wsl ? root + "\\" : root }];
  let cur = root.replace(/[\\/]$/, "");
  for (const part of rest.split(/[\\/]/).filter(Boolean)) {
    cur = cur + (cur.endsWith("\\") || cur.endsWith("/") ? "" : wsl || /^[A-Za-z]:/.test(path) ? "\\" : "/") + part;
    out.push({ label: part, path: cur });
  }
  return out;
}

export function FolderPicker({
  open,
  onOpenChange,
  initial,
  onPick,
  title = "Choose a project folder",
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  initial?: string;
  onPick: (path: string) => void;
  title?: string;
}) {
  const { data: roots } = useFetch<Roots>(open ? "/api/fs/roots" : null, [open]);
  const [path, setPath] = React.useState(initial ?? "");
  const [typed, setTyped] = React.useState(initial ?? "");
  const [hidden, setHidden] = React.useState(false);
  const [filter, setFilter] = React.useState("");
  const [listing, setListing] = React.useState<Listing | null>(null);
  const [err, setErr] = React.useState<string | null>(null);
  const [loading, setLoading] = React.useState(false);

  const go = React.useCallback(
    async (p: string) => {
      setLoading(true);
      setErr(null);
      try {
        const l = await api.get<Listing>("/api/fs/list" + qs({ path: p, hidden: hidden ? 1 : undefined }));
        setListing(l);
        setPath(l.path);
        setTyped(l.path);
        setFilter("");
      } catch (e) {
        setErr((e as Error).message);
      } finally {
        setLoading(false);
      }
    },
    [hidden],
  );

  // Open where it was asked to, or at home, each time the dialog opens.
  const home = roots?.home;
  React.useEffect(() => {
    if (!open || !home) return;
    let live = true;
    void Promise.resolve().then(() => {
      if (live) void go(initial || home);
    });
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, home]);

  const shown = (listing?.entries ?? []).filter((e) => !filter || e.name.toLowerCase().includes(filter.toLowerCase()));
  const where = describePath(path);

  const section = (title: string, places: Place[], Icon: React.ElementType) =>
    places.length === 0 ? null : (
      <div className="mb-3">
        <div className="mb-1 px-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{title}</div>
        {places.map((p) => {
          const target = placePath(p);
          return (
            <button
              key={title + target}
              onClick={() => void go(target)}
              title={p.detail ? `${p.detail}\n${target}` : target}
              className={cn(
                "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-muted",
                path.toLowerCase() === target.toLowerCase() && "bg-muted font-medium",
              )}
            >
              <Icon className="size-4 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate">{p.label}</span>
              {p.target?.startsWith("wsl:") && title === "Recent" && (
                <Badge variant="outline" className="h-4 px-1 text-[10px] font-normal">
                  wsl
                </Badge>
              )}
            </button>
          );
        })}
      </div>
    );

  return (
    <Dialog open={open} onOpenChange={(o) => onOpenChange(o)}>
      <DialogContent className="flex h-[78vh] max-h-[720px] flex-col gap-3 sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>Folders on this machine and inside every WSL distribution. A WSL folder runs its conversation inside that distribution, as the terminal does.</DialogDescription>
        </DialogHeader>

        <div className="flex min-h-0 flex-1 gap-3">
          <nav className="w-52 shrink-0 overflow-auto border-r pr-2">
            {section("Recent", roots?.recent ?? [], ClockIcon)}
            {section("This PC", [...(roots ? [{ label: "Home", path: roots.home }] : []), ...(roots?.drives ?? [])], HardDriveIcon)}
            {section("WSL", roots?.wsl ?? [], TerminalSquareIcon)}
            {!roots && <div className="px-2 text-xs text-muted-foreground">Looking for drives and WSL distributions…</div>}
          </nav>

          <div className="flex min-w-0 flex-1 flex-col gap-2">
            <form
              className="flex gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                void go(typed);
              }}
            >
              <Button type="button" size="icon-sm" variant="outline" title="Up a level" disabled={!listing?.parent} onClick={() => listing?.parent && void go(listing.parent)}>
                <ArrowUpIcon />
              </Button>
              <Input value={typed} onChange={(e) => setTyped(e.target.value)} className="h-7 font-mono text-xs" placeholder="Type or paste a path, then Enter" />
            </form>
            <div className="flex flex-wrap items-center gap-0.5 text-xs">
              {crumbs(path).map((c, i, all) => (
                <React.Fragment key={c.path + i}>
                  <button className={cn("rounded px-1 py-0.5 hover:bg-muted", i === all.length - 1 && "font-medium")} onClick={() => void go(c.path)}>
                    {c.label}
                  </button>
                  {i < all.length - 1 && <span className="text-muted-foreground">/</span>}
                </React.Fragment>
              ))}
            </div>
            <div className="flex items-center gap-2">
              <div className="relative flex-1">
                <SearchIcon className="absolute top-1.5 left-2 size-3.5 text-muted-foreground" />
                <Input
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && shown[0]) {
                      e.preventDefault();
                      void go(shown[0].path);
                    }
                  }}
                  placeholder="Filter — Enter opens the first match"
                  className="h-7 pl-7 text-xs"
                />
              </div>
              <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
                <input type="checkbox" checked={hidden} onChange={(e) => setHidden(e.target.checked)} /> hidden
              </label>
            </div>
            <ErrorNote error={err} />
            <div className={cn("min-h-0 flex-1 overflow-auto rounded-lg border", loading && "opacity-60")}>
              {shown.length === 0 && !loading && <div className="p-6 text-center text-sm text-muted-foreground">No folders here.</div>}
              {shown.map((e) => (
                <button
                  key={e.path}
                  onClick={() => void go(e.path)}
                  className="flex w-full items-center gap-2 border-b px-3 py-1.5 text-left text-sm last:border-b-0 hover:bg-muted/60"
                >
                  {e.project ? <FolderGit2Icon className="size-4 shrink-0 text-primary" /> : <FolderIcon className={cn("size-4 shrink-0 text-muted-foreground", e.hidden && "opacity-50")} />}
                  <span className={cn("min-w-0 flex-1 truncate", e.hidden && "text-muted-foreground")}>{e.name}</span>
                  {e.project && (
                    <Badge variant="secondary" className="h-4 px-1.5 text-[10px] font-normal">
                      project
                    </Badge>
                  )}
                </button>
              ))}
            </div>
          </div>
        </div>

        <DialogFooter className="items-center gap-3 sm:justify-between">
          <div className="min-w-0 text-xs">
            {where.where ? (
              <>
                <Badge variant="outline" className="mr-1.5 font-normal">
                  {where.where}
                </Badge>
                <span className="font-mono">{where.dir}</span>
              </>
            ) : (
              <span className="font-mono">{path}</span>
            )}
          </div>
          <Button
            disabled={!listing}
            onClick={() => {
              onPick(path);
              onOpenChange(false);
            }}
          >
            Use this folder
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
