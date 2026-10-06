"use client";

import * as React from "react";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { timeAgo, stamp } from "@/lib/format";
import { CopyIcon, CheckIcon, AlertTriangleIcon } from "lucide-react";

/** NativeSelect is a styled <select>: dependable, keyboard-friendly, no portal. */
export function NativeSelect({
  value,
  onChange,
  options,
  className,
  disabled,
  placeholder,
}: {
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string; disabled?: boolean }[];
  className?: string;
  disabled?: boolean;
  placeholder?: string;
}) {
  return (
    <select
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
      className={cn(
        "h-8 rounded-lg border border-input bg-background px-2 text-sm outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50 dark:bg-input/30",
        className,
      )}
    >
      {placeholder !== undefined && <option value="">{placeholder}</option>}
      {options.map((o) => (
        <option key={o.value} value={o.value} disabled={o.disabled}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

export function PageHeader({ title, description, actions }: { title: string; description?: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-3 border-b px-4 py-3 md:px-6 md:py-4">
      <div className="min-w-0">
        <h1 className="text-lg font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-0.5 text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Empty({ title, children, className }: { title: string; children?: React.ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-col items-center justify-center gap-1 rounded-xl border border-dashed p-8 text-center", className)}>
      <div className="text-sm font-medium">{title}</div>
      {children && <div className="max-w-md text-sm text-muted-foreground">{children}</div>}
    </div>
  );
}

export function ErrorNote({ error, className }: { error?: string | null; className?: string }) {
  if (!error) return null;
  return (
    <div className={cn("flex items-start gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive", className)}>
      <AlertTriangleIcon className="mt-0.5 size-4 shrink-0" />
      <span className="break-words">{error}</span>
    </div>
  );
}

export function Ago({ at, className }: { at?: string; className?: string }) {
  const [, tick] = React.useState(0);
  React.useEffect(() => {
    const t = setInterval(() => tick((n) => n + 1), 30000);
    return () => clearInterval(t);
  }, []);
  if (!at) return null;
  return (
    <time className={cn("whitespace-nowrap text-muted-foreground", className)} title={stamp(at)} dateTime={at}>
      {timeAgo(at)}
    </time>
  );
}

export function ScopeBadge({ scope }: { scope: string }) {
  return (
    <Badge variant={scope === "global" ? "secondary" : "outline"} className="font-normal">
      {scope === "global" ? "global" : "project"}
    </Badge>
  );
}

export function Field({ label, hint, children, className }: { label: string; hint?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <label className={cn("flex flex-col gap-1.5", className)}>
      <span className="text-sm font-medium">{label}</span>
      {children}
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </label>
  );
}

export function Mono({ children, className }: { children: React.ReactNode; className?: string }) {
  return <code className={cn("rounded bg-muted px-1 py-0.5 font-mono text-[12px]", className)}>{children}</code>;
}

export function CopyButton({ text, className }: { text: string; className?: string }) {
  const [done, setDone] = React.useState(false);
  return (
    <button
      type="button"
      title="Copy"
      className={cn("inline-flex size-6 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground", className)}
      onClick={() => {
        void navigator.clipboard?.writeText(text);
        setDone(true);
        setTimeout(() => setDone(false), 1200);
      }}
    >
      {done ? <CheckIcon className="size-3.5" /> : <CopyIcon className="size-3.5" />}
    </button>
  );
}

/** Pre shows text as written, scrolling inside its own box. */
export function Pre({ children, className, max = "max-h-80" }: { children: React.ReactNode; className?: string; max?: string }) {
  return (
    <pre className={cn("overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-[12px] leading-relaxed whitespace-pre-wrap break-words", max, className)}>
      {children}
    </pre>
  );
}

/** Text renders an agent's prose: paragraphs kept, fenced code set apart. */
export function Text({ text, className }: { text: string; className?: string }) {
  const parts = React.useMemo(() => text.split(/(```[\s\S]*?(?:```|$))/g), [text]);
  return (
    <div className={cn("space-y-2 text-sm leading-relaxed", className)}>
      {parts.map((p, i) => {
        if (p.startsWith("```")) {
          const body = p.replace(/^```[^\n]*\n?/, "").replace(/```$/, "");
          return (
            <Pre key={i} max="max-h-96">
              {body}
            </Pre>
          );
        }
        if (!p.trim()) return null;
        return (
          <div key={i} className="whitespace-pre-wrap break-words">
            {p.trim()}
          </div>
        );
      })}
    </div>
  );
}

export function Dot({ on, pulse, className }: { on: boolean; pulse?: boolean; className?: string }) {
  return (
    <span className={cn("relative inline-flex size-2 shrink-0", className)}>
      {pulse && on && <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-400 opacity-60" />}
      <span className={cn("relative inline-flex size-2 rounded-full", on ? "bg-emerald-500" : "bg-muted-foreground/40")} />
    </span>
  );
}

export function Stat({ label, value, hint }: { label: string; value: React.ReactNode; hint?: React.ReactNode }) {
  return (
    <div className="rounded-xl border bg-card p-4">
      <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className="mt-1 text-2xl font-semibold tabular-nums">{value}</div>
      {hint && <div className="mt-1 text-xs text-muted-foreground">{hint}</div>}
    </div>
  );
}
