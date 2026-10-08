"use client";

import * as React from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { cn } from "@/lib/utils";
import { looksLikePath, useFileOpener } from "@/lib/file-opener";
import { CopyButton } from "@/components/common";
import { MermaidBlock } from "@/components/mermaid-block";
import { RunBox, RunButton, RunRoot } from "@/components/run-command";
import { asCommand, clauseAround, looksRunnable, shellLang } from "@/lib/run";

// Agent replies are Markdown — tables, headings, quotes, code — and read far
// better rendered. Links open in a new tab; code keeps a copy button; wide
// tables scroll inside their own box instead of stretching the transcript.
// A fenced block's language, read off the <code> inside a <pre>.
function languageOf(children: React.ReactNode): string {
  const kid = React.Children.toArray(children)[0];
  if (!React.isValidElement(kid)) return "";
  const cls = (kid.props as { className?: string }).className ?? "";
  return /language-([\w-]+)/.exec(cls)?.[1] ?? "";
}

const base: Components = {
  a: ({ href, children }) => (
    <a href={href} target="_blank" rel="noreferrer" className="text-primary underline underline-offset-2">
      {children}
    </a>
  ),
  table: ({ children }) => (
    <div className="my-3 overflow-x-auto rounded-lg border">
      <table className="my-0 w-full text-[13px]">{children}</table>
    </div>
  ),
  th: ({ children, style }) => (
    <th style={style} className="border-b bg-muted/60 px-2.5 py-1.5 text-left font-medium whitespace-nowrap">
      {children}
    </th>
  ),
  td: ({ children, style }) => (
    <td style={style} className="border-b px-2.5 py-1.5 align-top">
      {children}
    </td>
  ),
  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
  p: ({ children }) => <RunSlot as="p">{children}</RunSlot>,
  li: ({ children }) => <RunSlot as="li">{children}</RunSlot>,
  code: ({ className, children }) => {
    const block = /language-/.test(className ?? "") || String(children).includes("\n");
    if (block) return <code className={cn("font-mono", className)}>{children}</code>;
    return <InlineCode>{children}</InlineCode>;
  },
};

/** CodeBlock is a fenced block with a copy button, and a Run button when it
 * is a shell's (```bash, ```powershell) or reads as a command; the run's
 * output shows under it. */
function CodeBlock({ children }: { children?: React.ReactNode }) {
  const text = extractText(children);
  const lang = languageOf(children);
  const root = React.useContext(RunRoot);
  const [runs, setRuns] = React.useState<number[]>([]);
  const command = asCommand(text);
  const runnable = !!root && !!command.trim() && (shellLang(lang) || (!lang && looksRunnable(command.split("\n")[0])));
  return (
    <div className="my-3">
      <div className="group/code relative">
        <pre className="max-h-[32rem] overflow-auto rounded-lg border !bg-muted/50 !p-3 !pr-20 text-[12px] leading-relaxed text-foreground">{children}</pre>
        <div className="absolute top-1.5 right-1.5 flex items-center gap-1">
          {runnable && <RunButton onClick={() => setRuns((r) => [...r, Date.now()])} className="bg-background/90" />}
          <CopyButton text={text} className="bg-background/80 opacity-0 group-hover/code:opacity-100" />
        </div>
      </div>
      {runs.map((k) => (
        <RunBox key={k} command={command} lang={lang} onClose={() => setRuns((r) => r.filter((x) => x !== k))} />
      ))}
    </div>
  );
}

// A command in a sentence is run from the paragraph or list item it is in,
// and its output shows under that block: a box cannot sit inside a line of
// text.
type InlineRun = { key: number; command: string };
const SlotContext = React.createContext<{ run: (command: string) => void } | null>(null);

/** RunSlot is a paragraph or list item that can show runs under it. */
function RunSlot({ as: Tag, children }: { as: "p" | "li"; children?: React.ReactNode }) {
  const root = React.useContext(RunRoot);
  const [runs, setRuns] = React.useState<InlineRun[]>([]);
  const slot = React.useMemo(() => ({ run: (command: string) => setRuns((r) => [...r, { key: Date.now(), command }]) }), []);
  if (!root) return <Tag>{children}</Tag>;
  // What its clause says about where to run it ("on the Windows side").
  const text = runs.length ? extractText(children) : "";
  const boxes = runs.map((r) => (
    <RunBox key={r.key} command={r.command} context={clauseAround(text, r.command)} onClose={() => setRuns((rs) => rs.filter((x) => x.key !== r.key))} />
  ));
  if (Tag === "li")
    return (
      <SlotContext.Provider value={slot}>
        <li>
          {children}
          {boxes}
        </li>
      </SlotContext.Provider>
    );
  return (
    <SlotContext.Provider value={slot}>
      <p>{children}</p>
      {boxes}
    </SlotContext.Provider>
  );
}

/** InlineCode is a code span; one naming a project file opens it, in a session,
 * and one that reads as a command has a Run button after it. */
function InlineCode({ children }: { children?: React.ReactNode }) {
  const open = useFileOpener();
  const text = extractText(children);
  const slot = React.useContext(SlotContext);
  if (slot && !text.includes("\n") && looksRunnable(text)) {
    return (
      <span className="whitespace-nowrap">
        <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em] font-normal whitespace-normal before:content-none after:content-none">{children}</code>{" "}
        <RunButton onClick={() => slot.run(text)} label={false} className="px-1 py-[3px]" />
      </span>
    );
  }
  const cls = "rounded bg-muted px-1 py-0.5 font-mono text-[0.85em] font-normal before:content-none after:content-none";
  if (open && looksLikePath(text)) {
    return (
      <code
        role="link"
        tabIndex={0}
        title="Open in the project explorer"
        onClick={() => open(text)}
        onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && open(text)}
        className={cn(cls, "cursor-pointer text-sky-700 underline decoration-sky-500/40 underline-offset-2 hover:decoration-sky-500 dark:text-sky-300")}
      >
        {children}
      </code>
    );
  }
  return <code className={cls}>{children}</code>;
}

function extractText(node: React.ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(extractText).join("");
  if (React.isValidElement(node)) return extractText((node.props as { children?: React.ReactNode }).children);
  return "";
}

export function Markdown({ text, className, streaming }: { text: string; className?: string; streaming?: boolean }) {
  // Diagrams are drawn, not shown as code: a ```mermaid block becomes a
  // rendered diagram with its source a click away.
  const components = React.useMemo<Components>(
    () => ({
      ...base,
      pre: (props) => {
        if (languageOf(props.children) === "mermaid") {
          return <MermaidBlock code={extractText(props.children).replace(/\n$/, "")} streaming={streaming} />;
        }
        return base.pre ? (base.pre as (p: typeof props) => React.ReactNode)(props) : <pre>{props.children}</pre>;
      },
    }),
    [streaming],
  );
  return (
    <div
      className={cn(
        "prose prose-sm prose-neutral max-w-none [&_blockquote_p]:before:content-none [&_blockquote_p]:after:content-none break-words dark:prose-invert",
        "prose-headings:mt-4 prose-headings:mb-2 prose-headings:font-semibold prose-h1:text-lg prose-h2:text-base prose-h3:text-sm",
        "prose-p:my-2 prose-ul:my-2 prose-ol:my-2 prose-li:my-0.5 prose-blockquote:my-3 prose-blockquote:font-normal prose-blockquote:not-italic",
        "prose-hr:my-4 prose-pre:my-0 prose-pre:bg-transparent prose-pre:p-0",
        className,
      )}
    >
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {text}
      </ReactMarkdown>
    </div>
  );
}
