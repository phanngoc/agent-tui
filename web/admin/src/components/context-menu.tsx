"use client";

import * as React from "react";
import { ChevronRightIcon } from "lucide-react";
import { cn } from "@/lib/utils";

export type MenuItem =
  | {
      label: string;
      run?: () => void;
      danger?: boolean;
      /** hint is shown on the right; a single letter is also the item's key while the menu is open. */
      hint?: string;
      disabled?: boolean;
      /** items make this a submenu, opened to the side. */
      items?: MenuItem[];
    }
  | "-";

const WIDTH = 240;

/**
 * ContextMenu is the app's right-click menu: in the editor's explorer and tabs,
 * and on a conversation in the list. Arrow keys move, Enter or a hint letter
 * chooses, Escape closes.
 */
export function ContextMenu({ x, y, items, onClose }: { x: number; y: number; items: MenuItem[]; onClose: () => void }) {
  const ref = React.useRef<HTMLDivElement>(null);
  React.useEffect(() => {
    const close = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) onClose();
    };
    window.addEventListener("mousedown", close);
    window.addEventListener("blur", onClose);
    window.addEventListener("resize", onClose);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("blur", onClose);
      window.removeEventListener("resize", onClose);
    };
  }, [onClose]);
  return (
    <div ref={ref}>
      <MenuPanel x={x} y={y} items={items} onClose={onClose} root />
    </div>
  );
}

function tidy(items: MenuItem[]) {
  // No separator first, last, or twice in a row.
  const out: MenuItem[] = [];
  for (const it of items) {
    if (it === "-" && (out.length === 0 || out[out.length - 1] === "-")) continue;
    out.push(it);
  }
  while (out[out.length - 1] === "-") out.pop();
  return out;
}

function MenuPanel({ x, y, items, onClose, root, onBack }: { x: number; y: number; items: MenuItem[]; onClose: () => void; root?: boolean; onBack?: () => void }) {
  const rows = tidy(items);
  const panel = React.useRef<HTMLDivElement>(null);
  const [sub, setSub] = React.useState<{ i: number; x: number; y: number; keyboard: boolean } | null>(null);
  const height = rows.reduce((h, it) => h + (it === "-" ? 9 : 30), 8);
  const left = Math.max(4, Math.min(x, window.innerWidth - WIDTH - 4));
  const top = Math.max(4, Math.min(y, window.innerHeight - height - 8));

  React.useEffect(() => {
    // The panel takes the keyboard, so arrows and letters reach it.
    panel.current?.focus({ preventScroll: true });
  }, []);

  const buttons = () => Array.from(panel.current?.querySelectorAll<HTMLButtonElement>(":scope > button:not(:disabled)") ?? []);
  const openSub = (i: number, el: HTMLElement, keyboard: boolean) => {
    const r = el.getBoundingClientRect();
    const right = r.right + WIDTH > window.innerWidth;
    setSub({ i, x: right ? r.left - WIDTH : r.right - 2, y: r.top - 4, keyboard });
  };
  const choose = (it: Exclude<MenuItem, "-">, el: HTMLElement, i: number, keyboard: boolean) => {
    if (it.disabled) return;
    if (it.items) return openSub(i, el, keyboard);
    onClose();
    it.run?.();
  };

  const onKey = (e: React.KeyboardEvent) => {
    if (sub?.keyboard) return; // the open submenu has it
    const list = buttons();
    const at = list.indexOf(document.activeElement as HTMLButtonElement);
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      if (onBack) onBack();
      else onClose();
    } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const n = list.length;
      if (!n) return;
      const next = e.key === "ArrowDown" ? (at + 1) % n : (at - 1 + n) % n;
      list[at < 0 && e.key === "ArrowUp" ? n - 1 : next]?.focus();
    } else if (e.key === "ArrowLeft" && onBack) {
      e.preventDefault();
      onBack();
    } else if ((e.key === "ArrowRight" || e.key === "Enter" || e.key === " ") && at >= 0) {
      const i = Number(list[at].dataset.i);
      const it = rows[i];
      if (it === "-" || (e.key === "ArrowRight" && !it.items)) return;
      e.preventDefault();
      choose(it, list[at], i, true);
    } else if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      const i = rows.findIndex((it) => it !== "-" && !it.disabled && it.hint?.length === 1 && it.hint.toLowerCase() === e.key.toLowerCase());
      if (i < 0) return;
      e.preventDefault();
      const el = panel.current?.querySelector<HTMLElement>(`:scope > button[data-i="${i}"]`);
      if (el) choose(rows[i] as Exclude<MenuItem, "-">, el, i, true);
    }
  };

  const subItem = sub ? rows[sub.i] : null;
  return (
    <>
      <div
        ref={panel}
        role="menu"
        tabIndex={-1}
        onKeyDown={onKey}
        onContextMenu={(e) => e.preventDefault()}
        className={cn("fixed z-50 rounded-md border bg-popover py-1 text-[13px] text-popover-foreground shadow-lg outline-none", root ? "min-w-56" : "min-w-48")}
        style={{ left, top, maxWidth: WIDTH + 40 }}
      >
        {rows.map((it, i) =>
          it === "-" ? (
            <div key={"sep" + i} className="my-1 border-t" />
          ) : (
            <button
              key={it.label}
              data-i={i}
              role="menuitem"
              aria-haspopup={it.items ? "menu" : undefined}
              disabled={it.disabled}
              onMouseEnter={(e) => {
                e.currentTarget.focus({ preventScroll: true });
                if (it.items) openSub(i, e.currentTarget, false);
                else setSub(null);
              }}
              onClick={(e) => {
                e.stopPropagation();
                choose(it, e.currentTarget, i, false);
              }}
              className={cn(
                "flex w-full items-center gap-6 px-3 py-1.5 text-left outline-none focus:bg-primary focus:text-primary-foreground disabled:opacity-40",
                it.danger && "text-destructive",
                sub?.i === i && "bg-muted",
              )}
            >
              <span className="flex-1 truncate">{it.label}</span>
              {it.items ? <ChevronRightIcon className="size-3.5 opacity-70" /> : it.hint && <span className="text-[11px] opacity-60">{it.hint}</span>}
            </button>
          ),
        )}
      </div>
      {sub && subItem && subItem !== "-" && subItem.items && (
        <MenuPanel
          key={sub.i}
          x={sub.x}
          y={sub.y}
          items={subItem.items}
          onClose={onClose}
          onBack={() => {
            const i = sub.i;
            setSub(null);
            panel.current?.querySelector<HTMLElement>(`:scope > button[data-i="${i}"]`)?.focus();
          }}
        />
      )}
    </>
  );
}
