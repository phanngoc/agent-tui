"use client";

import * as React from "react";
import { CheckIcon, CodeIcon, CopyIcon, DownloadIcon, Maximize2Icon, MinusIcon, PlusIcon, ScanIcon, WorkflowIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { clashes, repairSequence } from "@/lib/mermaid-repair";

// A ```mermaid block rendered the way ChatGPT renders one: a card with the
// diagram's kind on its header, a Diagram / Code switch, copy, download and a
// full-screen view that zooms and pans. While a reply is still streaming the
// code shows until it parses; a block that never does falls back to its code
// with the parser's message, rather than an empty box.

type Mermaid = typeof import("mermaid").default;

let loading: Promise<Mermaid> | null = null;
let configured = "";

function load(): Promise<Mermaid> {
  loading ??= import("mermaid").then((m) => m.default);
  return loading;
}

const FONT = 'var(--font-sans), ui-sans-serif, system-ui, "Segoe UI", "Noto Sans JP", sans-serif';

/** palette is a quiet, neutral one, close to the page's own tokens. */
function palette(dark: boolean) {
  return dark
    ? {
        background: "transparent",
        fontFamily: FONT,
        fontSize: "14px",
        primaryColor: "#27272a",
        primaryTextColor: "#f4f4f5",
        primaryBorderColor: "#52525b",
        secondaryColor: "#1f2937",
        tertiaryColor: "#18181b",
        lineColor: "#a1a1aa",
        textColor: "#e4e4e7",
        mainBkg: "#27272a",
        nodeBorder: "#52525b",
        clusterBkg: "#18181b",
        clusterBorder: "#3f3f46",
        edgeLabelBackground: "#18181b",
        actorBkg: "#27272a",
        actorBorder: "#52525b",
        actorTextColor: "#f4f4f5",
        actorLineColor: "#52525b",
        signalColor: "#d4d4d8",
        signalTextColor: "#e4e4e7",
        labelBoxBkgColor: "#27272a",
        labelBoxBorderColor: "#52525b",
        labelTextColor: "#f4f4f5",
        loopTextColor: "#e4e4e7",
        noteBkgColor: "#3f3a1e",
        noteBorderColor: "#857a3a",
        noteTextColor: "#f4f4f5",
        activationBkgColor: "#3f3f46",
        activationBorderColor: "#71717a",
        sequenceNumberColor: "#18181b",
      }
    : {
        background: "transparent",
        fontFamily: FONT,
        fontSize: "14px",
        primaryColor: "#f4f4f5",
        primaryTextColor: "#18181b",
        primaryBorderColor: "#d4d4d8",
        secondaryColor: "#eef2ff",
        tertiaryColor: "#fafafa",
        lineColor: "#71717a",
        textColor: "#27272a",
        mainBkg: "#f4f4f5",
        nodeBorder: "#d4d4d8",
        clusterBkg: "#fafafa",
        clusterBorder: "#e4e4e7",
        edgeLabelBackground: "#ffffff",
        actorBkg: "#f4f4f5",
        actorBorder: "#d4d4d8",
        actorTextColor: "#18181b",
        actorLineColor: "#d4d4d8",
        signalColor: "#52525b",
        signalTextColor: "#27272a",
        labelBoxBkgColor: "#f4f4f5",
        labelBoxBorderColor: "#d4d4d8",
        labelTextColor: "#18181b",
        loopTextColor: "#3f3f46",
        noteBkgColor: "#fef9c3",
        noteBorderColor: "#eab308",
        noteTextColor: "#422006",
        activationBkgColor: "#e4e4e7",
        activationBorderColor: "#a1a1aa",
        sequenceNumberColor: "#ffffff",
      };
}

async function mermaidFor(dark: boolean): Promise<Mermaid> {
  const m = await load();
  const want = dark ? "dark" : "light";
  if (configured !== want) {
    m.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      theme: "base",
      look: "neo",
      fontFamily: FONT,
      themeVariables: palette(dark),
      // Plain SVG text rather than HTML labels: a diagram that has to be
      // saved as a picture cannot carry foreignObject into a canvas.
      htmlLabels: false,
      // At its own size, as ChatGPT shows one: a wide diagram scrolls
      // sideways instead of shrinking until its labels cannot be read.
      flowchart: { curve: "basis", padding: 12, useMaxWidth: false },
      sequence: { mirrorActors: false, messageFontSize: 13, noteFontSize: 13, actorFontSize: 13, wrap: true, width: 170, useMaxWidth: false },
      class: { useMaxWidth: false },
      state: { useMaxWidth: false },
      er: { useMaxWidth: false },
      gantt: { useMaxWidth: false },
      journey: { useMaxWidth: false },
    });
    configured = want;
  }
  return m;
}

const KIND: Record<string, string> = {
  sequence: "Sequence diagram",
  "flowchart-v2": "Flowchart",
  flowchart: "Flowchart",
  graph: "Flowchart",
  classDiagram: "Class diagram",
  class: "Class diagram",
  stateDiagram: "State diagram",
  state: "State diagram",
  er: "ER diagram",
  gantt: "Gantt chart",
  pie: "Pie chart",
  journey: "User journey",
  gitGraph: "Git graph",
  mindmap: "Mind map",
  timeline: "Timeline",
  quadrantChart: "Quadrant chart",
  requirement: "Requirement diagram",
  c4: "C4 diagram",
  sankey: "Sankey diagram",
  xychart: "XY chart",
  block: "Block diagram",
  architecture: "Architecture diagram",
  kanban: "Kanban",
  packet: "Packet diagram",
};

interface Result {
  svg?: string;
  kind?: string;
  error?: string;
  // repaired names the actors renamed to make the source render.
  repaired?: string[];
}

const cache = new Map<string, Result>();
let seq = 0;
// One render at a time: mermaid works in scratch elements on the page, and
// two renders interleaving — or one cleaning up after a failure — can pull
// the other's element out from under it.
let queue: Promise<unknown> = Promise.resolve();

/**
 * forTheme adapts colours a diagram chose for a light page. Highlight boxes
 * (`rect rgb(255,235,235)`) are pale fills meant for dark text; on a dark page
 * the text is light, so the fills become a faint tint of the same colour.
 */
function forTheme(code: string, dark: boolean): string {
  if (!dark) return code;
  return code.replace(/^(\s*rect\s+)rgb\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)/gim, "$1rgba($2, $3, $4, 0.14)");
}

async function attempt(m: Mermaid, code: string): Promise<Result> {
  const id = `mmd-${++seq}`;
  try {
    const parsed = await m.parse(code);
    const { svg } = await m.render(id, code);
    const type = parsed ? (parsed as { diagramType?: string }).diagramType : undefined;
    return { svg, kind: (type && KIND[type]) || "Diagram" };
  } catch (e) {
    // A failed render leaves its own scratch element behind; only that one goes.
    document.getElementById("d" + id)?.remove();
    document.getElementById(id)?.remove();
    return { error: (e as Error)?.message?.split("\n").slice(0, 4).join("\n") || String(e) };
  }
}

function renderDiagram(code: string, dark: boolean): Promise<Result> {
  const key = (dark ? "d:" : "l:") + code;
  const hit = cache.get(key);
  if (hit) return Promise.resolve(hit);
  const run = queue.then(async () => {
    const again = cache.get(key);
    if (again) return again;
    const m = await mermaidFor(dark);
    let out = await attempt(m, forTheme(code, dark));
    if (out.error) {
      const fixed = repairSequence(code);
      if (fixed !== code) {
        const second = await attempt(m, forTheme(fixed, dark));
        if (second.svg) out = { ...second, repaired: clashes(code) };
      }
    }
    if (cache.size > 200) cache.clear();
    cache.set(key, out);
    return out;
  });
  queue = run.catch(() => undefined);
  return run;
}

/** useDark follows the class on <html> that the theme toggle sets. */
function useDark(): boolean {
  return React.useSyncExternalStore(
    (cb) => {
      const o = new MutationObserver(cb);
      o.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
      return () => o.disconnect();
    },
    () => document.documentElement.classList.contains("dark"),
    () => false,
  );
}

function save(name: string, blob: Blob) {
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

async function savePNG(svg: string, dark: boolean) {
  const el = new DOMParser().parseFromString(svg, "image/svg+xml").documentElement as unknown as SVGSVGElement;
  const vb = el.viewBox?.baseVal;
  const w = Math.ceil(vb?.width || 1200);
  const h = Math.ceil(vb?.height || 800);
  el.setAttribute("width", String(w));
  el.setAttribute("height", String(h));
  const data = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(new XMLSerializer().serializeToString(el));
  const img = new Image();
  await new Promise<void>((ok, bad) => {
    img.onload = () => ok();
    img.onerror = () => bad(new Error("could not draw the diagram"));
    img.src = data;
  });
  const scale = 2;
  const c = document.createElement("canvas");
  c.width = w * scale;
  c.height = h * scale;
  const ctx = c.getContext("2d")!;
  ctx.fillStyle = dark ? "#09090b" : "#ffffff";
  ctx.fillRect(0, 0, c.width, c.height);
  ctx.scale(scale, scale);
  ctx.drawImage(img, 0, 0, w, h);
  c.toBlob((b) => b && save("diagram.png", b), "image/png");
}

function IconButton({ title, onClick, children, active }: { title: string; onClick: () => void; children: React.ReactNode; active?: boolean }) {
  return (
    <button
      type="button"
      title={title}
      onClick={onClick}
      className={cn(
        "inline-flex h-7 items-center gap-1 rounded-md px-1.5 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
        active && "bg-background text-foreground shadow-sm",
      )}
    >
      {children}
    </button>
  );
}

export function MermaidBlock({ code, streaming }: { code: string; streaming?: boolean }) {
  const dark = useDark();
  const [res, setRes] = React.useState<Result | null>(() => cache.get((dark ? "d:" : "l:") + code) ?? null);
  const [view, setView] = React.useState<"diagram" | "code">("diagram");
  const [copied, setCopied] = React.useState(false);
  const [full, setFull] = React.useState(false);
  const [menu, setMenu] = React.useState(false);

  React.useEffect(() => {
    let live = true;
    // Streaming text changes many times a second; wait for a pause.
    const t = setTimeout(
      () => {
        renderDiagram(code, dark).then((r) => {
          if (!live) return;
          // A half-written diagram mid-stream is not an error yet.
          if (r.error && streaming) return;
          setRes(r);
        });
      },
      streaming ? 600 : 0,
    );
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [code, dark, streaming]);

  const showCode = view === "code" || !res?.svg;

  return (
    <div className="not-prose my-4 overflow-hidden rounded-xl border bg-card text-card-foreground">
      <div className="flex items-center gap-2 border-b bg-muted/40 px-3 py-1">
        <WorkflowIcon className="size-3.5 text-muted-foreground" />
        <span className="text-xs font-medium">{res?.kind ?? (res?.error ? "Mermaid" : "Diagram")}</span>
        {!res && <span className="text-xs text-muted-foreground">rendering…</span>}
        <div className="ml-auto flex items-center gap-0.5">
          {res?.svg && (
            <div className="mr-1 flex rounded-lg bg-muted p-0.5">
              <IconButton title="Show the diagram" active={view === "diagram"} onClick={() => setView("diagram")}>
                <WorkflowIcon className="size-3.5" /> Diagram
              </IconButton>
              <IconButton title="Show the Mermaid source" active={view === "code"} onClick={() => setView("code")}>
                <CodeIcon className="size-3.5" /> Code
              </IconButton>
            </div>
          )}
          <IconButton
            title="Copy the Mermaid source"
            onClick={() => {
              void navigator.clipboard?.writeText(code);
              setCopied(true);
              setTimeout(() => setCopied(false), 1200);
            }}
          >
            {copied ? <CheckIcon className="size-3.5" /> : <CopyIcon className="size-3.5" />}
          </IconButton>
          {res?.svg && (
            <div className="relative">
              <IconButton title="Download" onClick={() => setMenu(!menu)}>
                <DownloadIcon className="size-3.5" />
              </IconButton>
              {menu && (
                <div className="absolute top-8 right-0 z-20 w-36 overflow-hidden rounded-lg border bg-popover text-xs shadow-md" onMouseLeave={() => setMenu(false)}>
                  <button
                    className="block w-full px-3 py-2 text-left hover:bg-muted"
                    onClick={() => {
                      save("diagram.svg", new Blob([res.svg!], { type: "image/svg+xml" }));
                      setMenu(false);
                    }}
                  >
                    SVG
                  </button>
                  <button
                    className="block w-full px-3 py-2 text-left hover:bg-muted"
                    onClick={() => {
                      void savePNG(res.svg!, dark);
                      setMenu(false);
                    }}
                  >
                    PNG
                  </button>
                  <button
                    className="block w-full px-3 py-2 text-left hover:bg-muted"
                    onClick={() => {
                      save("diagram.mmd", new Blob([code], { type: "text/plain" }));
                      setMenu(false);
                    }}
                  >
                    Mermaid source
                  </button>
                </div>
              )}
            </div>
          )}
          {res?.svg && (
            <IconButton title="Open full screen" onClick={() => setFull(true)}>
              <Maximize2Icon className="size-3.5" />
            </IconButton>
          )}
        </div>
      </div>

      {res?.repaired && view === "diagram" && (
        <div className="border-b bg-sky-500/10 px-3 py-1.5 text-xs text-sky-800 dark:text-sky-300">
          Rendered after renaming {res.repaired.join(", ")}, which Mermaid reads as a keyword. The source below is unchanged.
        </div>
      )}
      {res?.error && !streaming && (
        <div className="border-b bg-amber-500/10 px-3 py-1.5 text-xs text-amber-800 dark:text-amber-300">
          This diagram did not render — showing its source. <span className="font-mono opacity-80">{res.error.split("\n")[0]}</span>
        </div>
      )}

      {showCode ? (
        <pre className="max-h-[32rem] overflow-auto p-3 font-mono text-[12px] leading-relaxed">
          <code>{code}</code>
        </pre>
      ) : (
        <Inline svg={res!.svg!} onOpen={() => setFull(true)} />
      )}

      {res?.svg && <FullScreen open={full} onOpenChange={setFull} svg={res.svg} kind={res.kind ?? "Diagram"} dark={dark} />}
    </div>
  );
}

/** naturalSize reads the size mermaid drew a diagram at. */
function naturalSize(svg: SVGSVGElement): { w: number; h: number } {
  const vb = svg.viewBox?.baseVal;
  if (vb && vb.width > 0) return { w: vb.width, h: vb.height };
  return { w: parseFloat(svg.getAttribute("width") || "0") || 600, h: parseFloat(svg.getAttribute("height") || "0") || 400 };
}

/**
 * Inline shows a diagram in the transcript: shrunk to the column's width, but
 * never below 60% of its size — past that its labels stop being readable, so
 * a wider diagram scrolls sideways instead. Full screen is a click away.
 */
function Inline({ svg, onOpen }: { svg: string; onOpen: () => void }) {
  const box = React.useRef<HTMLButtonElement>(null);
  React.useLayoutEffect(() => {
    const el = box.current;
    const s = el?.querySelector("svg");
    if (!el || !s) return;
    const { w, h } = naturalSize(s);
    const fit = () => {
      const room = el.clientWidth - 32;
      const scale = Math.max(0.6, Math.min(1, room / w));
      s.setAttribute("width", String(Math.round(w * scale)));
      s.setAttribute("height", String(Math.round(h * scale)));
      s.style.maxWidth = "none";
    };
    fit();
    const ro = new ResizeObserver(fit);
    ro.observe(el);
    return () => ro.disconnect();
  }, [svg]);
  return (
    <button
      ref={box}
      type="button"
      title="Open full screen"
      onClick={onOpen}
      className="block max-h-[42rem] w-full cursor-zoom-in overflow-auto p-4 text-left [&_svg]:mx-auto"
      // The SVG comes from mermaid with securityLevel "strict": labels are
      // sanitised and no script or link survives into it.
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}

/** FullScreen shows a diagram at any size: wheel or buttons zoom, dragging pans. */
function FullScreen({ open, onOpenChange, svg, kind, dark }: { open: boolean; onOpenChange: (o: boolean) => void; svg: string; kind: string; dark: boolean }) {
  const [scale, setScale] = React.useState(1);
  const [pos, setPos] = React.useState({ x: 0, y: 0 });
  const drag = React.useRef<{ x: number; y: number; px: number; py: number } | null>(null);
  const reset = () => {
    setScale(1);
    setPos({ x: 0, y: 0 });
  };
  const zoom = (f: number) => setScale((s) => Math.min(8, Math.max(0.2, s * f)));

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) reset();
        onOpenChange(o);
      }}
    >
      <DialogContent className="flex h-[92vh] max-w-[96vw] flex-col gap-0 p-0 sm:max-w-[96vw]">
        <div className="flex items-center gap-2 border-b px-4 py-2">
          <WorkflowIcon className="size-4 text-muted-foreground" />
          <DialogTitle className="text-sm">{kind}</DialogTitle>
          <div className="mr-8 ml-auto flex items-center gap-1">
            <IconButton title="Zoom out" onClick={() => zoom(1 / 1.25)}>
              <MinusIcon className="size-4" />
            </IconButton>
            <span className="w-12 text-center text-xs tabular-nums text-muted-foreground">{Math.round(scale * 100)}%</span>
            <IconButton title="Zoom in" onClick={() => zoom(1.25)}>
              <PlusIcon className="size-4" />
            </IconButton>
            <IconButton title="Fit" onClick={reset}>
              <ScanIcon className="size-4" />
            </IconButton>
            <IconButton title="Download PNG" onClick={() => void savePNG(svg, dark)}>
              <DownloadIcon className="size-4" />
            </IconButton>
          </div>
        </div>
        <div
          className="relative min-h-0 flex-1 cursor-grab overflow-hidden bg-[radial-gradient(circle,var(--color-border)_1px,transparent_1px)] [background-size:16px_16px] active:cursor-grabbing"
          onWheel={(e) => zoom(e.deltaY < 0 ? 1.1 : 1 / 1.1)}
          onPointerDown={(e) => {
            (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
            drag.current = { x: e.clientX, y: e.clientY, px: pos.x, py: pos.y };
          }}
          onPointerMove={(e) => {
            const d = drag.current;
            if (d) setPos({ x: d.px + e.clientX - d.x, y: d.py + e.clientY - d.y });
          }}
          onPointerUp={() => (drag.current = null)}
          onDoubleClick={reset}
        >
          <div
            className="absolute inset-0 flex items-center justify-center p-8 [&_svg]:h-auto [&_svg]:max-h-full [&_svg]:max-w-full"
            style={{ transform: `translate(${pos.x}px, ${pos.y}px) scale(${scale})`, transformOrigin: "center" }}
            dangerouslySetInnerHTML={{ __html: svg }}
          />
        </div>
        <div className="border-t px-4 py-1.5 text-center text-[11px] text-muted-foreground">Scroll to zoom · drag to move · double-click to fit</div>
      </DialogContent>
    </Dialog>
  );
}
