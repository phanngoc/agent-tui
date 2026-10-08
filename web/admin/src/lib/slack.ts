// A piece of the conversation, made ready to paste into Slack — the web's
// side of the terminal's /copy (internal/ui/slack.go), by the same rules.
//
// The copy carries two things. HTML, which Slack's composer turns into its own
// formatting when it is pasted: bold, italics, strikes, links, lists, quotes,
// inline code and code blocks come through as themselves. And, for anywhere
// that takes only text, the same in Slack's mrkdwn, which reads well as plain
// text too.
//
// Slack has no headings and no tables. A heading becomes a bold line; a table
// becomes a code block with its columns aligned, the one grid every Slack
// client draws the same.
//
// What is copied is what was selected on the page, already rendered, so the
// walk is over the DOM rather than the Markdown: the selection can start mid
// paragraph and end in a table, and only what it covers goes. The page's own
// chrome — buttons, icons, a message's header and its row of actions — is
// left behind.

/** Elements that are the page, not the message. */
const SKIP = new Set(["BUTTON", "SVG", "svg", "SELECT", "INPUT", "TEXTAREA", "SCRIPT", "STYLE", "IMG", "VIDEO", "CANVAS", "NOSCRIPT"]);
const skipped = (el: Element) => SKIP.has(el.tagName) || el.hasAttribute("data-copy-skip") || el.getAttribute("aria-hidden") === "true" || el.classList.contains("xterm");

// zwsp lets a Slack marker sit against a letter: Slack reads an asterisk as
// bold only at a word boundary, and an invisible space makes one.
const zwsp = "​";
const wordish = (c: string | undefined) => !!c && /[\p{L}\p{N}]/u.test(c);

const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

/** One inline run, written both ways. */
type Run = { html: string; text: string };

const BLOCKS = new Set([
  "P",
  "DIV",
  "SECTION",
  "ARTICLE",
  "UL",
  "OL",
  "LI",
  "BLOCKQUOTE",
  "PRE",
  "TABLE",
  "THEAD",
  "TBODY",
  "TR",
  "H1",
  "H2",
  "H3",
  "H4",
  "H5",
  "H6",
  "HR",
  "DETAILS",
  "SUMMARY",
  "FIGURE",
]);
const isBlock = (n: Node) => n.nodeType === 1 && BLOCKS.has((n as Element).tagName);

/** inline writes the inline content of a node: text and its marks. */
function inline(node: Node): Run {
  if (node.nodeType === Node.TEXT_NODE) {
    const t = (node.textContent ?? "").replace(/\s+/g, " ");
    return { html: esc(t), text: t };
  }
  if (node.nodeType !== Node.ELEMENT_NODE) return { html: "", text: "" };
  const el = node as Element;
  if (skipped(el)) return { html: "", text: "" };
  const kids = () => {
    const parts = Array.from(el.childNodes).map(inline);
    return { html: parts.map((p) => p.html).join(""), text: parts.map((p) => p.text).join("") };
  };
  switch (el.tagName) {
    case "BR":
      return { html: "<br>", text: "\n" };
    case "CODE": {
      const t = el.textContent ?? "";
      return t ? { html: `<code>${esc(t)}</code>`, text: "`" + t + "`" } : { html: "", text: "" };
    }
    case "STRONG":
    case "B":
      return mark(kids(), "b", "*");
    case "EM":
    case "I":
      return mark(kids(), "i", "_");
    case "DEL":
    case "S":
    case "STRIKE":
      return mark(kids(), "s", "~");
    case "A": {
      const href = el.getAttribute("href") ?? "";
      const k = kids();
      if (!/^(https?:|mailto:)/i.test(href)) return k;
      const label = k.text.trim();
      return { html: `<a href="${esc(href)}">${k.html || esc(href)}</a>`, text: !label || label === href ? href : `<${href}|${label}>` };
    }
    default:
      return kids();
  }
}

/** mark wraps a run in bold, italics or a strike. Spaces at its edges stay
 * outside the marker, where Slack needs them. */
function mark(r: Run, tag: string, m: string): Run {
  const lead = r.text.match(/^\s*/)![0];
  const trail = r.text.match(/\s*$/)![0];
  const inner = r.text.trim();
  if (!inner) return r;
  return { html: `<${tag}>${r.html}</${tag}>`, text: `${lead}${OPEN}${m}${inner}${m}${CLOSE}${trail}` };
}

// Where a marker opens and closes, until the text is whole and its
// neighbours are known.
const OPEN = "\u0001";
const CLOSE = "\u0002";

/** glue settles the markers: one touching a letter outside it ("đã*xong*")
 * gets the invisible space Slack needs to read it; the rest stay as they are. */
function glue(text: string): string {
  return text.replace(/[\u0001\u0002]/g, (c, i: number) => {
    const outside = c === OPEN ? text[i - 1] : text[i + 1];
    return wordish(outside) ? zwsp : "";
  });
}

/** A block of the output: its HTML and its mrkdwn lines. */
type Block = { html: string; text: string };

/** blocks writes the block content of a node, with depth for nested lists. */
function blocks(node: Node, depth = 0): Block[] {
  const out: Block[] = [];
  let line: Run[] = [];
  const flush = () => {
    const html = line
      .map((r) => r.html)
      .join("")
      .trim();
    const text = line
      .map((r) => r.text)
      .join("")
      .replace(/[ \t]+\n/g, "\n")
      .trim();
    if (text) out.push({ html: `<p>${html}</p>`, text });
    line = [];
  };
  for (const child of Array.from(node.childNodes)) {
    if (!isBlock(child)) {
      if (child.nodeType === 1 && skipped(child as Element)) continue;
      line.push(inline(child));
      continue;
    }
    flush();
    out.push(...block(child as Element, depth));
  }
  flush();
  return out;
}

function block(el: Element, depth: number): Block[] {
  if (skipped(el)) return [];
  switch (el.tagName) {
    case "H1":
    case "H2":
    case "H3":
    case "H4":
    case "H5":
    case "H6": {
      const r = inline(el);
      const t = r.text.trim();
      return t ? [{ html: `<p><b>${r.html.trim()}</b></p>`, text: `*${t}*` }] : [];
    }
    case "HR":
      return [{ html: `<p>${"─".repeat(24)}</p>`, text: "─".repeat(24) }];
    case "PRE": {
      const code = Array.from(el.querySelectorAll("code"))[0]?.textContent ?? el.textContent ?? "";
      const t = code.replace(/\n+$/, "");
      return t.trim() ? [{ html: `<pre>${esc(t)}</pre>`, text: "```\n" + t + "\n```" }] : [];
    }
    case "BLOCKQUOTE": {
      const inner = blocks(el, depth);
      if (!inner.length) return [];
      return [
        {
          // Lines joined by breaks, as the terminal's copy does: a paragraph
          // inside a quote can split it in two when Slack pastes it.
          html: `<blockquote>${inner.map((b) => b.html.replace(/^<p>([\s\S]*)<\/p>$/, "$1")).join("<br>")}</blockquote>`,
          text: inner
            .map((b) => b.text)
            .join("\n")
            .split("\n")
            .map((l) => "> " + l)
            .join("\n"),
        },
      ];
    }
    case "UL":
    case "OL":
      return [list(el, depth)];
    case "LI":
      // A list item without its list: what a selection starting inside a
      // list gives.
      return [list(wrapIn("UL", el), depth)];
    case "TABLE":
    case "THEAD":
    case "TBODY":
    case "TR":
      return table(el);
    default:
      return blocks(el, depth);
  }
}

function wrapIn(tag: string, el: Element): Element {
  const w = document.createElement(tag);
  w.appendChild(el.cloneNode(true));
  return w;
}

/** list writes a list, numbered or not, with its nested lists indented. */
function list(el: Element, depth: number): Block {
  const ordered = el.tagName === "OL";
  const start = Number(el.getAttribute("start") ?? 1) || 1;
  const html: string[] = [];
  const text: string[] = [];
  let n = start;
  for (const li of Array.from(el.children)) {
    if (li.tagName !== "LI") continue;
    // The item's own words, then any list inside it. Inline pieces run on as
    // they are; a paragraph inside the item is set off from them by a space.
    const own: Run[] = [];
    const nested: Block[] = [];
    for (const c of Array.from(li.childNodes)) {
      if (c.nodeType === 1 && ((c as Element).tagName === "UL" || (c as Element).tagName === "OL")) nested.push(list(c as Element, depth + 1));
      else if (isBlock(c)) {
        for (const b of block(c as Element, depth + 1)) own.push({ html: " " + b.html.replace(/^<p>|<\/p>$/g, ""), text: " " + b.text });
      } else own.push(inline(c));
    }
    const words = own
      .map((r) => r.text)
      .join("")
      .replace(/\s+/g, " ")
      .trim();
    const pad = "    ".repeat(depth);
    const bullet = ordered ? `${n++}.` : depth % 2 ? "◦" : "•";
    text.push(`${pad}${bullet} ${words}`);
    for (const b of nested) text.push(b.text);
    html.push(`<li>${own.map((r) => r.html).join("").trim()}${nested.map((b) => b.html).join("")}</li>`);
  }
  const tag = ordered ? "ol" : "ul";
  return { html: `<${tag}${ordered && start !== 1 ? ` start="${start}"` : ""}>${html.join("")}</${tag}>`, text: text.join("\n") };
}

/** table writes a table as a code block with its columns aligned. */
function table(el: Element): Block[] {
  const rows = (el.tagName === "TR" ? [el] : Array.from(el.querySelectorAll("tr"))).map((tr) =>
    Array.from(tr.children)
      .filter((c) => c.tagName === "TD" || c.tagName === "TH")
      .map((c) => (c.textContent ?? "").replace(/\s+/g, " ").trim()),
  );
  const real = rows.filter((r) => r.some(Boolean));
  if (!real.length) return [];
  // One cell is not a table: what was picked from inside a cell is its text.
  if (real.length === 1 && real[0].filter(Boolean).length === 1) {
    const cell = Array.from(el.querySelectorAll("td, th")).find((c) => (c.textContent ?? "").trim()) ?? el;
    return blocks(cell);
  }
  const cols = Math.max(...real.map((r) => r.length));
  const width = (s: string) => Array.from(s).reduce((w, ch) => w + (/[ᄀ-ᅟ⺀-꓏가-힣豈-﫿︰-﹏＀-｠￠-￦]/.test(ch) ? 2 : 1), 0);
  const widths = Array.from({ length: cols }, (_, i) => Math.max(...real.map((r) => width(r[i] ?? ""))));
  const fmt = (r: string[]) =>
    widths
      .map((w, i) => (r[i] ?? "") + " ".repeat(w - width(r[i] ?? "")))
      .join("  ")
      .trimEnd();
  // Too wide for a grid that reads in a Slack message: a row per bullet
  // instead, its first cell in bold and the rest each named by its column.
  if (widths.reduce((a, w) => a + w + 2, 0) > WIDE) return [tableAsList(el)];
  const lines = real.map(fmt);
  // A rule under the header row, when there is one.
  if (el.querySelector("th") && lines.length > 1) lines.splice(1, 0, widths.map((w) => "-".repeat(w)).join("  "));
  const t = lines.join("\n");
  return [{ html: `<pre>${esc(t)}</pre>`, text: "```\n" + t + "\n```" }];
}

/** How wide a table can be and still be sent as an aligned grid. */
const WIDE = 90;

/** tableAsList writes a wide table as a list, one row an item:
 * "*mng#2186* — Status: Open · Note: …". Cells keep their own marks. */
function tableAsList(el: Element): Block {
  const trs = el.tagName === "TR" ? [el] : Array.from(el.querySelectorAll("tr"));
  const cells = (tr: Element) => Array.from(tr.children).filter((c) => c.tagName === "TD" || c.tagName === "TH");
  const headRow = trs.find((tr) => cells(tr).some((c) => c.tagName === "TH"));
  const heads = headRow ? cells(headRow).map((c) => (c.textContent ?? "").replace(/\s+/g, " ").trim()) : [];
  const html: string[] = [];
  const text: string[] = [];
  for (const tr of trs) {
    if (tr === headRow) continue;
    const runs = cells(tr).map((c) => inline(c));
    if (!runs.some((r) => r.text.trim())) continue;
    const empty = (r: Run) => !r.text.trim() || /^[—–-]$/.test(r.text.trim());
    const rest = runs.slice(1).map((r, i) => ({ r, head: heads[i + 1] ?? "" })).filter((x) => !empty(x.r));
    // The first cell is set in bold whole, so its own marks are dropped.
    const lead = (cells(tr)[0]?.textContent ?? "").replace(/\s+/g, " ").trim();
    const first = { text: lead, html: esc(lead) };
    const t = [`${OPEN}*${first.text}*${CLOSE}`, rest.map((x) => (x.head ? `${x.head}: ` : "") + x.r.text.trim()).join(" · ")].filter(Boolean).join(" — ");
    const h = [`<b>${first.html}</b>`, rest.map((x) => (x.head ? `${esc(x.head)}: ` : "") + x.r.html.trim()).join(" · ")].filter(Boolean).join(" — ");
    text.push(`• ${t}`);
    html.push(`<li>${h}</li>`);
  }
  return { html: `<ul>${html.join("")}</ul>`, text: text.join("\n") };
}

/** slackFrom renders a selection's contents for Slack: HTML and mrkdwn. */
export function slackFrom(root: Node): { html: string; text: string } {
  const out = blocks(root);
  const text = out
    .map((b) => b.text)
    .join("\n\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
  return { html: out.map((b) => b.html).join(""), text: glue(text) };
}

/** copyRich puts HTML and text on the clipboard as one item, so each place
 * it is pasted takes the form it understands. */
export async function copyRich(html: string, text: string): Promise<void> {
  if (typeof ClipboardItem !== "undefined" && navigator.clipboard?.write) {
    try {
      await navigator.clipboard.write([new ClipboardItem({ "text/html": new Blob([html], { type: "text/html" }), "text/plain": new Blob([text], { type: "text/plain" }) })]);
      return;
    } catch {
      // Not allowed here (an insecure origin, say): the copy event below is.
    }
  }
  let done = false;
  const onCopy = (e: ClipboardEvent) => {
    e.clipboardData?.setData("text/html", html);
    e.clipboardData?.setData("text/plain", text);
    e.preventDefault();
    done = true;
  };
  document.addEventListener("copy", onCopy, { once: true });
  document.execCommand("copy");
  document.removeEventListener("copy", onCopy);
  if (!done) throw new Error("the browser would not copy");
}
