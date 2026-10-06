"use client";

import * as React from "react";
import { createPortal } from "react-dom";
import { PanelLeftCloseIcon, PanelLeftOpenIcon, PanelRightCloseIcon, PanelRightOpenIcon, XIcon } from "lucide-react";
import { Group, Panel, Separator, useDefaultLayout, usePanelRef, type PanelImperativeHandle } from "react-resizable-panels";
import { cn } from "@/lib/utils";
import { isTyping, safeStorage, useLayout } from "@/lib/layout";
import { useIsMobile } from "@/lib/mobile";

// A page of panes the reader arranges: a list on the left, the thing being
// worked on in the middle, details on the right. Side panes drag wider or
// narrower, fold away (by their button, a shortcut, or dragging them shut)
// and come back at the width they had; the arrangement is remembered per
// page. Focus mode folds them all.
//
// On a phone there is room for one pane: the thing being worked on fills the
// screen, and the list and the side panel open over it, full screen, from
// the same toggles. Picking something in the list (the page's selection
// changing) closes the list; with nothing picked, the list is what shows.

interface Side {
  node: React.ReactNode;
  /** Pixels. */
  defaultSize: number;
  minSize: number;
  maxSize: number;
  /** Start folded when the window is narrower than this. */
  foldBelow?: number;
  label: string;
}

interface WorkspaceCtx {
  leftOpen: boolean;
  rightOpen: boolean;
  hasLeft: boolean;
  hasRight: boolean;
  toggleLeft: () => void;
  toggleRight: () => void;
  openRight: () => void;
  /** rightSlot is the right pane's element, for content rendered further down the tree. */
  rightSlot: HTMLElement | null;
}

const Ctx = React.createContext<WorkspaceCtx>({
  leftOpen: false,
  rightOpen: false,
  hasLeft: false,
  hasRight: false,
  toggleLeft: () => {},
  toggleRight: () => {},
  openRight: () => {},
  rightSlot: null,
});

export const useWorkspace = () => React.useContext(Ctx);

function toggle(ref: React.RefObject<PanelImperativeHandle | null>) {
  const p = ref.current;
  if (!p) return;
  if (p.isCollapsed()) p.expand();
  else p.collapse();
}

export function Workspace(props: { id: string; left?: Side; right?: Side; children: React.ReactNode; selection?: string }) {
  const mobile = useIsMobile();
  return mobile ? <MobileWorkspace {...props} /> : <DesktopWorkspace {...props} />;
}

function MobileWorkspace({ left, right, children, selection }: { left?: Side; right?: Side; children: React.ReactNode; selection?: string }) {
  const [leftOpen, setLeftOpen] = React.useState(() => !!left && !selection);
  const [rightOpen, setRightOpen] = React.useState(false);
  const [rightSlot, setRightSlot] = React.useState<HTMLElement | null>(null);
  // Picking something closes the list: state adjusted while rendering, when
  // the selection is not the one last seen.
  const [seen, setSeen] = React.useState(selection);
  if (selection !== seen) {
    setSeen(selection);
    if (selection) setLeftOpen(false);
  }
  const ctx: WorkspaceCtx = {
    leftOpen,
    rightOpen,
    hasLeft: !!left,
    hasRight: !!right,
    toggleLeft: () => {
      setRightOpen(false);
      setLeftOpen((o) => !o);
    },
    toggleRight: () => {
      setLeftOpen(false);
      setRightOpen((o) => !o);
    },
    openRight: () => {
      setLeftOpen(false);
      setRightOpen(true);
    },
    rightSlot,
  };
  return (
    <Ctx.Provider value={ctx}>
      <div className="relative h-full min-h-0">
        <div className="flex h-full min-h-0 min-w-0 flex-col">{children}</div>
        {left && (
          <div className={cn("absolute inset-0 z-30 flex flex-col bg-background", !leftOpen && "hidden")}>
            <SheetBar label={left.label} onClose={() => setLeftOpen(false)} />
            <div className="min-h-0 flex-1 overflow-hidden">{left.node}</div>
          </div>
        )}
        {right && (
          // Always mounted, so what the page portals into it stays put.
          <div className={cn("absolute inset-0 z-30 flex flex-col bg-background", !rightOpen && "hidden")}>
            <SheetBar label={right.label} onClose={() => setRightOpen(false)} />
            <div ref={setRightSlot} className="flex min-h-0 flex-1 flex-col overflow-hidden">
              {right.node}
            </div>
          </div>
        )}
      </div>
    </Ctx.Provider>
  );
}

function SheetBar({ label, onClose }: { label: string; onClose: () => void }) {
  return (
    <div className="flex h-11 shrink-0 items-center border-b px-3">
      <span className="text-sm font-medium capitalize">{label}</span>
      <button type="button" onClick={onClose} aria-label={`Close the ${label}`} className="ml-auto inline-flex size-9 items-center justify-center rounded-md text-muted-foreground hover:bg-muted">
        <XIcon className="size-5" />
      </button>
    </div>
  );
}

function DesktopWorkspace({ id, left, right, children }: { id: string; left?: Side; right?: Side; children: React.ReactNode }) {
  const leftRef = usePanelRef();
  const rightRef = usePanelRef();
  const [leftOpen, setLeftOpen] = React.useState(true);
  const [rightOpen, setRightOpen] = React.useState(true);
  const [rightSlot, setRightSlot] = React.useState<HTMLElement | null>(null);
  const focus = useLayout((s) => s.focus);
  const ids = [left && "left", "main", right && "right"].filter(Boolean) as string[];
  const { defaultLayout, onLayoutChanged } = useDefaultLayout({ id: "agent-tui.ws." + id, panelIds: ids, storage: safeStorage });

  const toggleLeft = React.useCallback(() => toggle(leftRef), [leftRef]);
  const toggleRight = React.useCallback(() => toggle(rightRef), [rightRef]);
  const openRight = React.useCallback(() => {
    if (rightRef.current?.isCollapsed()) rightRef.current.expand();
  }, [rightRef]);

  // A narrow window starts with the side panes folded.
  React.useEffect(() => {
    const w = window.innerWidth;
    if (right?.foldBelow && w < right.foldBelow) rightRef.current?.collapse();
    if (left?.foldBelow && w < left.foldBelow) leftRef.current?.collapse();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Focus mode folds everything and puts back what was open before.
  const before = React.useRef<{ l: boolean; r: boolean } | null>(null);
  React.useEffect(() => {
    const l = leftRef.current;
    const r = rightRef.current;
    if (focus) {
      before.current = { l: !!l && !l.isCollapsed(), r: !!r && !r.isCollapsed() };
      l?.collapse();
      r?.collapse();
    } else if (before.current) {
      if (before.current.l) l?.expand();
      if (before.current.r) r?.expand();
      before.current = null;
    }
  }, [focus, leftRef, rightRef]);

  // Alt+[ and Alt+] fold the left and right panes, outside a text field.
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!e.altKey || e.ctrlKey || e.metaKey || isTyping(e)) return;
      if (e.code === "BracketLeft" && left) {
        e.preventDefault();
        toggleLeft();
      } else if (e.code === "BracketRight" && right) {
        e.preventDefault();
        toggleRight();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [left, right, toggleLeft, toggleRight]);

  const ctx: WorkspaceCtx = { leftOpen, rightOpen, hasLeft: !!left, hasRight: !!right, toggleLeft, toggleRight, openRight, rightSlot };

  return (
    <Ctx.Provider value={ctx}>
      <Group id={"ws-" + id} orientation="horizontal" defaultLayout={defaultLayout} onLayoutChanged={onLayoutChanged} className="h-full min-h-0">
        {left && (
          <>
            <Panel
              id="left"
              panelRef={leftRef}
              collapsible
              collapsedSize={0}
              defaultSize={left.defaultSize}
              minSize={left.minSize}
              maxSize={left.maxSize}
              onResize={(s) => setLeftOpen(s.inPixels > 1)}
              className="min-w-0"
            >
              <div className="h-full min-h-0 overflow-hidden">{left.node}</div>
            </Panel>
            <Handle label={left.label} />
          </>
        )}
        <Panel id="main" minSize={360} className="min-w-0">
          <div className="flex h-full min-h-0 min-w-0 flex-col">{children}</div>
        </Panel>
        {right && (
          <>
            <Handle label={right.label} />
            <Panel
              id="right"
              panelRef={rightRef}
              collapsible
              collapsedSize={0}
              defaultSize={right.defaultSize}
              minSize={right.minSize}
              maxSize={right.maxSize}
              onResize={(s) => setRightOpen(s.inPixels > 1)}
              className="min-w-0"
            >
              <div ref={setRightSlot} className="flex h-full min-h-0 flex-col overflow-hidden">
                {right.node}
              </div>
            </Panel>
          </>
        )}
      </Group>
    </Ctx.Provider>
  );
}

/** Handle is the line between panes: drag it, or double-click to reset. */
function Handle({ label }: { label: string }) {
  return (
    <Separator
      title={`Drag to resize the ${label} · drag it shut to fold it`}
      className={cn(
        "group relative w-px shrink-0 bg-border outline-none transition-colors",
        "after:absolute after:inset-y-0 after:-left-1.5 after:w-3 after:content-['']",
        "hover:bg-primary/40 data-[separator=active]:bg-primary focus-visible:bg-primary",
      )}
    >
      <span className="absolute top-1/2 left-1/2 h-8 w-1 -translate-x-1/2 -translate-y-1/2 rounded-full bg-border opacity-0 transition-opacity group-hover:opacity-100" />
    </Separator>
  );
}

/** PaneToggle is the button that folds or unfolds a side pane. */
export function PaneToggle({ side, className }: { side: "left" | "right"; className?: string }) {
  const ws = useWorkspace();
  if (side === "left" ? !ws.hasLeft : !ws.hasRight) return null;
  const open = side === "left" ? ws.leftOpen : ws.rightOpen;
  const Icon = side === "left" ? (open ? PanelLeftCloseIcon : PanelLeftOpenIcon) : open ? PanelRightCloseIcon : PanelRightOpenIcon;
  const key = side === "left" ? "Alt+[" : "Alt+]";
  return (
    <button
      type="button"
      onClick={side === "left" ? ws.toggleLeft : ws.toggleRight}
      title={`${open ? "Hide" : "Show"} the ${side === "left" ? "list" : "side panel"} (${key})`}
      className={cn("inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground", className)}
    >
      <Icon className="size-4" />
    </button>
  );
}

/** RightPane renders its children into the workspace's right pane. */
export function RightPane({ children }: { children: React.ReactNode }) {
  const { rightSlot } = useWorkspace();
  return rightSlot ? createPortal(children, rightSlot) : null;
}
