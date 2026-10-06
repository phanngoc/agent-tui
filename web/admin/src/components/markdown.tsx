"use client";

import * as React from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { cn } from "@/lib/utils";
import { looksLikePath, useFileOpener } from "@/lib/file-opener";
import { CopyButton } from "@/components/common";
import { MermaidBlock } from "@/components/mermaid-block";

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
  pre: ({ children }) => {
    const text = extractText(children);
    return (
      <div className="group/code relative my-3">
        <pre className="max-h-[32rem] overflow-auto rounded-lg border bg-muted/50 p-3 text-[12px] leading-relaxed text-foreground">{children}</pre>
        <CopyButton text={text} className="absolute top-1.5 right-1.5 bg-background/80 opacity-0 group-hover/code:opacity-100" />
      </div>
    );
  },
  code: ({ className, children }) => {
    const block = /language-/.test(className ?? "") || String(children).includes("\n");
    if (block) return <code className={cn("font-mono", className)}>{children}</code>;
    return <InlineCode>{children}</InlineCode>;
  },
};

/** InlineCode is a code span; one naming a project file opens it, in a session. */
function InlineCode({ children }: { children?: React.ReactNode }) {
  const open = useFileOpener();
  const text = extractText(children);
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
