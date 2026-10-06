"use client";

import { api, qs } from "@/lib/api";
import { FuzzyIndex } from "@/lib/fuzzy";

// The project's file list for quick open: fetched once per project and kept,
// indexed for fuzzy matching, and fetched again when files are created,
// renamed or deleted, or when it is half a minute old. While a fresh list is
// on its way the old one answers, so quick open never waits on a refresh.

let cache: { root: string; at: number; index: Promise<FuzzyIndex>; fetching: boolean } | null = null;
const MAX_AGE = 30_000;

function fetchIndex(root: string) {
  return api.get<{ files: string[] }>("/api/files/all" + qs({ root })).then((r) => new FuzzyIndex(r.files));
}

export function fileIndex(root: string): Promise<FuzzyIndex> {
  if (!cache || cache.root !== root) {
    const index = fetchIndex(root);
    cache = { root, at: Date.now(), index, fetching: false };
    index.catch(() => {
      if (cache?.index === index) cache = null;
    });
    return index;
  }
  if (Date.now() - cache.at >= MAX_AGE && !cache.fetching) {
    const c = cache;
    c.fetching = true;
    fetchIndex(root)
      .then((ix) => {
        if (cache === c) {
          c.index = Promise.resolve(ix);
          c.at = Date.now();
        }
      })
      .catch(() => undefined)
      .finally(() => {
        c.fetching = false;
      });
  }
  return cache.index;
}

export function invalidateFiles() {
  if (cache) cache.at = 0;
}
