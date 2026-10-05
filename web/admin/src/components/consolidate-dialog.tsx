"use client";

import { CheckCircle2Icon, MinusCircleIcon, XCircleIcon } from "lucide-react";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { ScopeBadge } from "@/components/common";

export interface ConsolidateReport {
  dir: string;
  scope: "global" | "project";
  records: number;
  folded: number;
  scenes_before: number;
  scenes_after: number;
  persona: boolean;
  note?: string;
  error?: string;
  project?: string;
  project_name: string;
}

/** ConsolidateDialog says what “Consolidate” did, store by store. */
export function ConsolidateDialog({ report, onClose }: { report: ConsolidateReport[] | null; onClose: () => void }) {
  const changed = (report ?? []).filter((r) => !r.error && (r.folded > 0 || r.persona)).length;
  return (
    <Dialog open={!!report} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Consolidated</DialogTitle>
          <DialogDescription>
            {changed} of {report?.length ?? 0} stores rebuilt. Records fold into scenes (L2); the scenes are distilled into the persona or the project doctrine (L3). See
            them on Scenes &amp; persona.
          </DialogDescription>
        </DialogHeader>
        <div className="divide-y rounded-xl border">
          {(report ?? []).map((r) => (
            <div key={r.dir} className="flex items-start gap-3 px-3 py-2.5 text-sm">
              {r.error ? (
                <XCircleIcon className="mt-0.5 size-4 shrink-0 text-destructive" />
              ) : r.folded > 0 || r.persona ? (
                <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-emerald-600" />
              ) : (
                <MinusCircleIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
              )}
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <ScopeBadge scope={r.scope} />
                  <span className="truncate font-medium" title={r.project || r.dir}>
                    {r.scope === "global" ? "global" : r.project_name}
                  </span>
                  <span className="ml-auto shrink-0 text-xs text-muted-foreground">{r.records} records</span>
                </div>
                <div className="mt-0.5 text-xs text-muted-foreground">
                  {r.error ? (
                    <span className="text-destructive">{r.error}</span>
                  ) : r.note ? (
                    r.note
                  ) : (
                    <>
                      folded {r.folded} memories · scenes {r.scenes_before} → {r.scenes_after}
                      {r.persona && <> · {r.scope === "global" ? "persona" : "doctrine"} rewritten</>}
                    </>
                  )}
                </div>
              </div>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
