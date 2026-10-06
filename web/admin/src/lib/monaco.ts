"use client";

import type * as Monaco from "monaco-editor";
import pkg from "../../node_modules/monaco-editor/package.json";

// Monaco — VS Code's editor — is loaded at runtime from the prebuilt copy in
// public/monaco/<version>/vs (scripts/copy-monaco.mjs) with its own AMD
// loader, once per page, and only by the editor. The app's bundle carries none
// of it; the import above is types only.

export type MonacoNS = typeof Monaco;

let loading: Promise<MonacoNS> | null = null;

type AmdRequire = {
  (deps: string[], ok: () => void, err?: (e: unknown) => void): void;
  config: (c: { paths: Record<string, string> }) => void;
};

export function loadMonaco(): Promise<MonacoNS> {
  if (loading) return loading;
  loading = new Promise<MonacoNS>((resolve, reject) => {
    const w = window as unknown as { monaco?: MonacoNS; require?: AmdRequire };
    if (w.monaco) return resolve(w.monaco);
    const base = `${location.origin}/monaco/${pkg.version}/vs`;
    const script = document.createElement("script");
    script.src = base + "/loader.js";
    script.async = true;
    script.onload = () => {
      const req = w.require;
      if (!req) return reject(new Error("Monaco's loader did not start"));
      req.config({ paths: { vs: base } });
      req(["vs/editor/editor.main"], () => (w.monaco ? resolve(w.monaco) : reject(new Error("Monaco did not load"))), reject);
    };
    script.onerror = () => reject(new Error("Monaco could not be loaded from " + base));
    document.head.appendChild(script);
  });
  loading.catch(() => {
    loading = null;
  });
  return loading;
}

/** languageFor picks Monaco's language for a file, by name, then extension. */
export function languageFor(monaco: MonacoNS, path: string): string {
  const name = path.split("/").pop() ?? path;
  const lower = name.toLowerCase();
  const special: Record<string, string> = { dockerfile: "dockerfile", makefile: "makefile", "go.mod": "go", "go.sum": "plaintext", ".env": "ini" };
  if (special[lower]) return special[lower];
  if (lower.startsWith("dockerfile")) return "dockerfile";
  const ext = lower.includes(".") ? lower.slice(lower.lastIndexOf(".")) : "";
  for (const l of monaco.languages.getLanguages()) {
    if (l.filenames?.some((f) => f.toLowerCase() === lower)) return l.id;
    if (ext && l.extensions?.some((e) => e.toLowerCase() === ext)) return l.id;
  }
  return "plaintext";
}
