"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { MousePointerClickIcon } from "lucide-react";
import { useGateway } from "@/lib/store";
import { PaneToggle, Workspace } from "@/components/workspace";
import { FocusButton } from "@/components/focus-button";
import { NewChat } from "@/components/new-chat";
import { SessionList } from "@/components/session-list";
import { Conversation } from "@/components/conversation";

export default function SessionsPage() {
  return (
    <React.Suspense fallback={<div className="p-6 text-sm text-muted-foreground">Loading…</div>}>
      <Sessions />
    </React.Suspense>
  );
}

function Sessions() {
  const params = useSearchParams();
  const router = useRouter();
  const id = params.get("id") ?? "";
  const root = useGateway((s) => s.root);
  // Each "new" is a fresh draft, even when one is already on screen.
  const [fresh, setFresh] = React.useState(0);
  const list = (
    <SessionList
      selected={id}
      onSelect={(s) => router.push(`/sessions?id=${s}`)}
      onNew={() => {
        setFresh((n) => n + 1);
        router.push("/sessions");
      }}
    />
  );
  return (
    <Workspace selection={id || (fresh ? `new-${fresh}` : "")}
      id="sessions"
      left={{ node: list, defaultSize: 300, minSize: 220, maxSize: 520, foldBelow: 960, label: "session list" }}
      right={{
        node: id ? null : <SidePlaceholder />,
        defaultSize: 400,
        minSize: 300,
        maxSize: 720,
        foldBelow: 1400,
        label: "side panel",
      }}
    >
      {id ? (
        <Conversation key={id} id={id} />
      ) : (
        <>
          <div className="flex items-center gap-1 px-3 pt-2">
            <PaneToggle side="left" />
            <div className="ml-auto flex items-center gap-1">
              <FocusButton />
              <PaneToggle side="right" />
            </div>
          </div>
          <NewChat key={root + fresh} root={root} onCreated={(sid) => router.push(`/sessions?id=${sid}`)} />
        </>
      )}
    </Workspace>
  );
}

/** SidePlaceholder fills the side panel before a conversation is chosen. */
function SidePlaceholder() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-sm text-muted-foreground">
      <MousePointerClickIcon className="size-5" />
      Once a conversation starts, this panel shows what each turn was given — recalled memories, skills, MCP tools — what it taught the agent, and its
      details.
      <span className="text-xs">Fold it with Alt+] when you want the room.</span>
    </div>
  );
}
