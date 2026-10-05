// Models write sequence diagrams whose actors are named like the grammar's
// keywords — "AND" for Android is "and", the par keyword, and the grammar does
// not care about case — and the whole diagram then fails to parse. The repair
// renames such actors (AND → AND_), keeping the displayed name with "as", and
// touches only actor positions: the text of messages and notes stays exactly
// as written. It runs only after a diagram has failed to render.

const RESERVED = new Set([
  "and", "end", "loop", "alt", "else", "opt", "par", "critical", "break", "rect", "box", "note", "over",
  "title", "autonumber", "activate", "deactivate", "create", "destroy", "links", "link", "properties",
  "details", "option", "participant", "actor", "left", "right", "of",
]);
const ARROW = /(<<-{1,2}>>|-{1,2}>>|-{1,2}>|-{1,2}x|-{1,2}\))([+-]?)/;
const STATEMENT = /^(\s*(?:note\s+(?:over|left of|right of)|activate|deactivate|destroy|create\s+participant|create\s+actor)\s+)([^:]*)(.*)$/i;
const DECLARE = /^(\s*(?:participant|actor)\s+)(\S+)(.*)$/i;

/** clashes lists the actor names of a sequence diagram that read as keywords. */
export function clashes(code: string): string[] {
  if (!/^\s*sequenceDiagram/m.test(code)) return [];
  const bad = new Set<string>();
  const consider = (n: string) => {
    n = n.trim();
    if (n && RESERVED.has(n.toLowerCase())) bad.add(n);
  };
  for (const line of code.split("\n")) {
    let m = DECLARE.exec(line);
    if (m) consider(m[2]);
    m = STATEMENT.exec(line);
    if (m) m[2].split(",").forEach(consider);
    const a = arrowOf(line);
    if (a) {
      consider(a.head.slice(0, a.m.index));
      consider(a.head.slice(a.m.index + a.m[0].length));
    }
  }
  return [...bad];
}

function arrowOf(line: string) {
  // Everything before the first colon is the statement; after it, free text.
  const i = line.indexOf(":");
  const head = i < 0 ? line : line.slice(0, i);
  const m = ARROW.exec(head);
  return m ? { m, head, tail: i < 0 ? "" : line.slice(i) } : null;
}

/** repairSequence renames the clashing actors; anything else comes back as is. */
export function repairSequence(code: string): string {
  const bad = new Set(clashes(code));
  if (bad.size === 0) return code;
  const swap = (part: string) => part.replace(/^(\s*)(.*?)(\s*)$/, (_, a: string, name: string, b: string) => a + (bad.has(name) ? name + "_" : name) + b);
  return code
    .split("\n")
    .map((line) => {
      let m = DECLARE.exec(line);
      if (m) {
        if (!bad.has(m[2])) return line;
        const alias = /^\s+as\s+/i.test(m[3]) ? m[3] : " as " + m[2] + m[3];
        return m[1] + m[2] + "_" + alias;
      }
      m = STATEMENT.exec(line);
      if (m) return m[1] + m[2].split(",").map(swap).join(",") + m[3];
      const a = arrowOf(line);
      if (a) {
        const left = a.head.slice(0, a.m.index);
        const right = a.head.slice(a.m.index + a.m[0].length);
        return swap(left) + a.m[0] + swap(right) + a.tail;
      }
      return line;
    })
    .join("\n");
}
