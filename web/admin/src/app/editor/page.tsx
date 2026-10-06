"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { FolderOpenIcon } from "lucide-react";
import { useGateway } from "@/lib/store";
import { FolderPicker } from "@/components/folder-picker";
import { Button } from "@/components/ui/button";
import { Workbench } from "@/components/editor/workbench";
import { useEditor } from "@/components/editor/store";
import { editorService } from "@/components/editor/service";

// /editor?root=<project>&path=<file>&line=<n>: the project's code in a
// VS Code-like editor. Without a root it edits the project picked at the top.

export default function EditorPage() {
  return (
    <React.Suspense fallback={null}>
      <Editor />
    </React.Suspense>
  );
}

function Editor() {
  const params = useSearchParams();
  const router = useRouter();
  const picked = useGateway((s) => s.root);
  const root = params.get("root") || picked;
  const setRoot = useEditor((s) => s.setRoot);
  const current = useEditor((s) => s.root);
  const [picking, setPicking] = React.useState(false);

  React.useEffect(() => {
    if (!root || root === current) return;
    if (current && editorService.anyDirty() && !confirm("Switch projects and lose unsaved changes?")) return;
    editorService.reset();
    setRoot(root);
  }, [root, current, setRoot]);

  const path = params.get("path");
  const line = Number(params.get("line")) || undefined;
  React.useEffect(() => {
    if (path && current === root && root) void editorService.open(path, { line });
  }, [path, line, current, root]);

  if (!root) {
    return (
      <div className="grid h-full place-items-center">
        <div className="text-center">
          <div className="mb-2 text-lg font-medium">Open a project to edit</div>
          <p className="mb-4 text-sm text-muted-foreground">Pick a folder — on this machine or inside a WSL distribution.</p>
          <Button onClick={() => setPicking(true)}>
            <FolderOpenIcon /> Open Folder…
          </Button>
          <FolderPicker open={picking} onOpenChange={setPicking} onPick={(p) => router.replace(`/editor?root=${encodeURIComponent(p)}`)} title="Open a project in the editor" />
        </div>
      </div>
    );
  }
  if (current !== root) return null;
  return <Workbench />;
}
