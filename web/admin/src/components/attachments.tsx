"use client";

import * as React from "react";
import { Loader2Icon, PaperclipIcon, XIcon } from "lucide-react";
import { toast } from "sonner";
import { gatewayBase, qs } from "@/lib/api";
import { cn } from "@/lib/utils";
import { ImageViewer } from "@/components/image-viewer";

// Images for a prompt: dropped on the composer, pasted into it, or picked
// with the paperclip. Each is uploaded to the gateway at once
// (POST /api/attachments) and goes with the prompt by the path it is given.

export interface Attached {
  path: string;
  media: string;
  bytes: number;
  name: string;
  preview: string;
}

/** The files to send with the prompt, as the API takes them. */
export const filesOf = (items: Attached[]) => items.map(({ path, media, bytes }) => ({ path, media, bytes }));

const images = (list: FileList | File[] | null | undefined) => Array.from(list ?? []).filter((f) => f.type.startsWith("image/"));

const hasFiles = (e: React.DragEvent | DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes("Files");

export function useAttachments() {
  const [items, setItems] = React.useState<Attached[]>([]);
  const [uploading, setUploading] = React.useState(0);
  const [dragging, setDragging] = React.useState(false);
  const itemsRef = React.useRef(items);
  React.useEffect(() => {
    itemsRef.current = items;
  }, [items]);

  // A picture dropped beside the composer would have the browser open it in
  // place of the page, losing what was typed.
  React.useEffect(() => {
    const stop = (e: DragEvent) => {
      if (hasFiles(e)) e.preventDefault();
    };
    window.addEventListener("dragover", stop);
    window.addEventListener("drop", stop);
    return () => {
      window.removeEventListener("dragover", stop);
      window.removeEventListener("drop", stop);
      itemsRef.current.forEach((a) => URL.revokeObjectURL(a.preview));
    };
  }, []);

  const add = React.useCallback(async (files: File[]) => {
    for (const file of files) {
      setUploading((n) => n + 1);
      try {
        const fd = new FormData();
        fd.append("file", file, file.name || "pasted.png");
        const res = await fetch(gatewayBase() + "/api/attachments", { method: "POST", body: fd });
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(body.error || res.statusText);
        setItems((cur) => [...cur, { path: body.path, media: body.media, bytes: body.bytes, name: file.name || "image", preview: URL.createObjectURL(file) }]);
      } catch (e) {
        toast.error(`Could not attach ${file.name || "the image"}: ${(e as Error).message}`);
      } finally {
        setUploading((n) => n - 1);
      }
    }
  }, []);

  const remove = (i: number) =>
    setItems((cur) => {
      URL.revokeObjectURL(cur[i]?.preview ?? "");
      return cur.filter((_, j) => j !== i);
    });
  const clear = () =>
    setItems((cur) => {
      cur.forEach((a) => URL.revokeObjectURL(a.preview));
      return [];
    });

  /** dropProps go on the composer's box; onPaste on its text field. */
  const dropProps = {
    onDragOver: (e: React.DragEvent) => {
      if (!hasFiles(e)) return;
      e.preventDefault();
      setDragging(true);
    },
    onDragLeave: (e: React.DragEvent) => {
      if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false);
    },
    onDrop: (e: React.DragEvent) => {
      if (!hasFiles(e)) return;
      e.preventDefault();
      setDragging(false);
      const got = images(e.dataTransfer.files);
      if (got.length) void add(got);
      else toast.error("Only images can be attached");
    },
  };
  const onPaste = (e: React.ClipboardEvent) => {
    const got = images(e.clipboardData?.files);
    if (got.length) {
      e.preventDefault();
      void add(got);
    }
  };
  return { items, uploading, dragging, add, remove, clear, dropProps, onPaste };
}

const kb = (bytes: number) => `${Math.round(bytes / 1024)} KB`;

/** AttachmentStrip shows what will go with the prompt, each with a × — and
 * opens one large on a click, to check it before sending. */
export function AttachmentStrip({ items, uploading, onRemove, className }: { items: Attached[]; uploading: number; onRemove: (i: number) => void; className?: string }) {
  const [viewing, setViewing] = React.useState<number | null>(null);
  if (!items.length && !uploading) return null;
  return (
    <div className={cn("flex flex-wrap gap-2", className)}>
      {items.map((a, i) => (
        <div key={a.path} className="group relative size-14 overflow-hidden rounded-lg border bg-muted" title={`${a.name} · ${kb(a.bytes)} · click to enlarge`}>
          <button type="button" onClick={() => setViewing(i)} aria-label={`View ${a.name}`} className="size-full cursor-zoom-in">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={a.preview} alt={a.name} className="size-full object-cover" />
          </button>
          <button
            type="button"
            onClick={() => onRemove(i)}
            aria-label={`Remove ${a.name}`}
            className="absolute top-0.5 right-0.5 rounded-full bg-background/90 p-0.5 text-foreground opacity-90 shadow hover:opacity-100"
          >
            <XIcon className="size-3" />
          </button>
        </div>
      ))}
      {uploading > 0 && (
        <div className="flex size-14 items-center justify-center rounded-lg border border-dashed text-muted-foreground">
          <Loader2Icon className="size-4 animate-spin" />
        </div>
      )}
      <ImageViewer images={items.map((a) => ({ src: a.preview, name: a.name, note: kb(a.bytes) }))} index={viewing} onIndex={setViewing} />
    </div>
  );
}

/** AttachButton picks images from disk (or the phone's photos). */
export function AttachButton({ onPick, className }: { onPick: (files: File[]) => void; className?: string }) {
  const input = React.useRef<HTMLInputElement>(null);
  return (
    <>
      <button
        type="button"
        onClick={() => input.current?.click()}
        title="Attach images — or drop them here, or paste"
        aria-label="Attach images"
        className={cn("inline-flex size-7 shrink-0 items-center justify-center rounded-full border bg-background text-muted-foreground hover:bg-muted hover:text-foreground", className)}
      >
        <PaperclipIcon className="size-3.5" />
      </button>
      <input
        ref={input}
        type="file"
        accept="image/*"
        multiple
        hidden
        onChange={(e) => {
          const got = images(e.target.files);
          if (got.length) onPick(got);
          e.target.value = "";
        }}
      />
    </>
  );
}

/** DropHint covers the composer while an image is dragged over it. */
export function DropHint({ show }: { show: boolean }) {
  if (!show) return null;
  return (
    <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-[inherit] border-2 border-dashed border-primary bg-background/80 text-sm font-medium text-primary">
      Drop images to attach
    </div>
  );
}

/** attachmentURL is a sent image, as the gateway serves it back. */
export const attachmentURL = (path: string) => gatewayBase() + "/api/attachments" + qs({ path });
