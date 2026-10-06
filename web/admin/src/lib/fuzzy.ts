// Fuzzy file matching for quick open, in the page, over the whole project's
// file list. It runs on every keystroke across tens of thousands of paths, so
// it is a single pass per path with no allocation on a miss: the query must
// appear as a subsequence; the score rewards matches in the file name, at the
// start of a word or path segment, and in runs, and gently prefers short paths.

export type Match = { path: string; score: number; hits: number[] };

const sep = (c: number) => c === 47 /* / */ || c === 95 /* _ */ || c === 45 /* - */ || c === 46 /* . */ || c === 32;

function scoreOne(path: string, lower: string, q: string): number {
  const nameStart = path.lastIndexOf("/") + 1;
  let qi = 0;
  let score = 0;
  let run = 0;
  let last = -2;
  for (let i = 0; i < lower.length && qi < q.length; i++) {
    if (lower.charCodeAt(i) !== q.charCodeAt(qi)) {
      run = 0;
      continue;
    }
    let s = 1;
    if (i === last + 1) {
      run++;
      s += 4 * run;
    } else run = 0;
    const prev = i > 0 ? lower.charCodeAt(i - 1) : 47;
    if (i === 0 || sep(prev)) s += 6;
    else if (path.charCodeAt(i) >= 65 && path.charCodeAt(i) <= 90 && !(path.charCodeAt(i - 1) >= 65 && path.charCodeAt(i - 1) <= 90)) s += 4; // camelHump
    if (i >= nameStart) s += 3;
    score += s;
    last = i;
    qi++;
  }
  if (qi < q.length) return -1;
  // Whole query inside the file name, and at its start: what someone typing a
  // file's name means.
  const name = lower.slice(nameStart);
  const at = name.indexOf(q);
  if (at === 0) score += 30;
  else if (at > 0) score += 15;
  return score - lower.length * 0.05;
}

function hitsOf(lower: string, q: string): number[] {
  const nameStart = lower.lastIndexOf("/") + 1;
  const name = lower.slice(nameStart);
  const at = name.indexOf(q);
  if (at >= 0) return Array.from({ length: q.length }, (_, k) => nameStart + at + k);
  const out: number[] = [];
  let qi = 0;
  for (let i = 0; i < lower.length && qi < q.length; i++) {
    if (lower.charCodeAt(i) === q.charCodeAt(qi)) {
      out.push(i);
      qi++;
    }
  }
  return out;
}

/** Index precomputes the lower-cased paths once per file list. */
export class FuzzyIndex {
  private paths: string[];
  private lowers: string[];
  // The paths the last query matched. Typing narrows: a query that extends
  // the last one can only match among these, so each keystroke scans fewer.
  private last: { q: string; idx: Int32Array } | null = null;
  constructor(paths: string[]) {
    this.paths = paths;
    this.lowers = paths.map((p) => p.toLowerCase());
  }
  get size() {
    return this.paths.length;
  }
  /** search returns the best `limit` matches, best first. */
  search(query: string, limit = 60): Match[] {
    const q = query.toLowerCase().replace(/\s+/g, "").replace(/\\/g, "/");
    if (!q) return this.paths.slice(0, limit).map((path) => ({ path, score: 0, hits: [] }));
    const from = this.last && q.startsWith(this.last.q) ? this.last.idx : null;
    const n = from ? from.length : this.lowers.length;
    const matched = new Int32Array(n);
    let m = 0;
    // A small top-k kept sorted: cheaper than sorting every match.
    const top: { i: number; s: number }[] = [];
    let floor = -Infinity;
    for (let k = 0; k < n; k++) {
      const i = from ? from[k] : k;
      const s = scoreOne(this.paths[i], this.lowers[i], q);
      if (s < 0) continue;
      matched[m++] = i;
      if (top.length === limit && s <= floor) continue;
      let j = top.length;
      while (j > 0 && top[j - 1].s < s) j--;
      top.splice(j, 0, { i, s });
      if (top.length > limit) top.pop();
      floor = top.length === limit ? top[top.length - 1].s : -Infinity;
    }
    this.last = { q, idx: matched.subarray(0, m) };
    return top.map(({ i, s }) => ({ path: this.paths[i], score: s, hits: hitsOf(this.lowers[i], q) }));
  }
}
