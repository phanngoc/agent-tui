"use client";

import { Badge } from "@/components/ui/badge";

/** OwnerBadge says who runs a session: a terminal holding it, or the gateway. */
export function OwnerBadge({ owner }: { owner?: string }) {
  if (!owner) return null;
  const tui = owner.startsWith("tui");
  return (
    <Badge variant={tui ? "default" : "secondary"} className="font-normal" title={owner}>
      {tui ? "in terminal" : "gateway"}
    </Badge>
  );
}
