export function timeAgo(iso?: string): string {
  if (!iso) return "";
  const t = new Date(iso).getTime();
  if (!t || t < 0) return "";
  const s = Math.round((Date.now() - t) / 1000);
  if (s < 5) return "just now";
  if (s < 60) return `${s}s ago`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h}h ago`;
  const d = Math.round(h / 24);
  if (d < 60) return `${d}d ago`;
  return new Date(iso).toLocaleDateString();
}

export function stamp(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (!d.getTime()) return "";
  return d.toLocaleString();
}

/** baseName is the last segment of a Windows or POSIX path. */
export function baseName(p: string): string {
  if (!p) return "";
  const parts = p.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] ?? p;
}

export function nanos(n?: number): string {
  if (!n) return "";
  const ms = n / 1e6;
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  return `${Math.floor(s / 60)}m${Math.round(s % 60)}s`;
}

export function tokens(n?: number): string {
  if (!n) return "0";
  if (n < 1000) return String(n);
  if (n < 1e6) return `${(n / 1000).toFixed(1)}k`;
  return `${(n / 1e6).toFixed(2)}M`;
}

export function pretty(v: unknown): string {
  if (v === undefined || v === null) return "";
  if (typeof v === "string") {
    try {
      return JSON.stringify(JSON.parse(v), null, 2);
    } catch {
      return v;
    }
  }
  return JSON.stringify(v, null, 2);
}

/** toolSummary is the one-line gist of a tool call's input. */
export function toolSummary(input: unknown): string {
  if (!input || typeof input !== "object") return "";
  const o = input as Record<string, unknown>;
  for (const k of ["command", "path", "query", "name", "question", "task_id", "url"]) {
    if (typeof o[k] === "string") return String(o[k]).split("\n")[0].slice(0, 120);
  }
  const first = Object.values(o).find((v) => typeof v === "string") as string | undefined;
  return first ? first.split("\n")[0].slice(0, 120) : "";
}

export const memoryTypeLabel: Record<string, string> = {
  persona: "Persona",
  instruction: "Instruction",
  episodic: "Episode",
  work_fact: "Fact",
  work_task: "Task",
  work_method: "Method",
  work_artifact: "Artifact",
};

export const memoryTypeHint: Record<string, string> = {
  persona: "A lasting trait, preference or habit of the user",
  instruction: "A standing rule for the agent",
  episodic: "Something that happened, with when",
  work_fact: "A decision, requirement, constraint, risk or result",
  work_task: "Agreed follow-up work that is not done",
  work_method: "How things are done: SOP, principle, anti-pattern",
  work_artifact: "A file, document, endpoint or command that matters",
};
