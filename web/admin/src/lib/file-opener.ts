"use client";

import * as React from "react";

// A conversation's paths open in the project explorer: the session page
// provides the opener, and anything that shows a path — a reply's inline
// code, a tool call — asks for it. Outside a session there is none, and
// paths stay plain text.

/** The file opener a conversation hands its paths to; null outside a session. */
export const FileOpener = React.createContext<((p: string) => void) | null>(null);

export function useFileOpener() {
  return React.useContext(FileOpener);
}

const pathish = /^[\w.@~+\-/\\:]+$/;
const knownExt =
  /\.(md|mdx|go|ts|tsx|js|jsx|mjs|cjs|py|rb|rs|java|kt|php|cs|c|h|cpp|hpp|sh|ps1|sql|json|ya?ml|toml|ini|env|conf|xml|html|css|scss|txt|log|csv|mod|sum|lock|gradle|properties|dockerfile|tf|proto|graphql|svg|png|jpe?g|gif|webp)(:\d+(:\d+)?)?$/i;

/** looksLikePath says an inline code span is probably a file in the project. */
export function looksLikePath(s: string) {
  const t = s.trim();
  if (t.length < 3 || t.length > 300 || !pathish.test(t) || /^https?:/.test(t)) return false;
  return knownExt.test(t) || (/[/\\]/.test(t) && /\.[A-Za-z0-9]{1,8}(:\d+)?$/.test(t));
}
