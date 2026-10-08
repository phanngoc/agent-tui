"use client";

import * as React from "react";
import { ChevronLeftIcon, ChevronRightIcon, ExternalLinkIcon, XIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";

export interface ViewedImage {
  src: string;
  name?: string;
  /** note: a line under the name, such as the size. */
  note?: string;
}

/**
 * ImageViewer shows one of a set of images large, over the page: fitted to
 * the window, or at its own size (click the image to switch, then scroll).
 * With more than one, the arrows on screen or ←/→ step through them; Esc or
 * a click outside closes it.
 */
export function ImageViewer({ images, index, onIndex }: { images: ViewedImage[]; index: number | null; onIndex: (i: number | null) => void }) {
  const [actual, setActual] = React.useState(false);
  const open = index !== null && index >= 0 && index < images.length;
  const img = open ? images[index] : undefined;
  const many = images.length > 1;
  const step = React.useCallback(
    (d: number) => {
      if (index === null) return;
      setActual(false);
      onIndex((index + d + images.length) % images.length);
    },
    [index, images.length, onIndex],
  );
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) {
          setActual(false);
          onIndex(null);
        }
      }}
    >
      <DialogContent
        showCloseButton={false}
        className="w-auto max-w-[96vw] gap-2 bg-neutral-950 p-2 text-neutral-100 ring-white/10 sm:max-w-[96vw]"
        onKeyDown={(e) => {
          if (!many) return;
          if (e.key === "ArrowRight") step(1);
          else if (e.key === "ArrowLeft") step(-1);
        }}
      >
        <DialogTitle className="sr-only">{img?.name || "Image"}</DialogTitle>
        {/* The stock close sits on the picture and vanishes on a light one;
            this one stays on the frame, outside what scrolls. */}
        <button
          type="button"
          aria-label="Close"
          onClick={() => {
            setActual(false);
            onIndex(null);
          }}
          className="absolute top-3 right-3 z-10 rounded-full bg-black/70 p-1.5 text-white shadow ring-1 ring-white/20 hover:bg-black/90"
        >
          <XIcon className="size-4" />
        </button>
        {img && (
          <>
            {/* Centred while it fits; at its own size it starts top-left, since a
                centred box that overflows cannot be scrolled to its left edge. */}
            <div className={cn("relative", actual ? "h-[84vh] w-[94vw] overflow-auto" : "flex items-center justify-center")}>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                src={img.src}
                alt={img.name ?? ""}
                onClick={() => setActual(!actual)}
                className={cn("rounded-md", actual ? "max-w-none cursor-zoom-out" : "max-h-[84vh] max-w-[94vw] cursor-zoom-in object-contain")}
              />
              {many && !actual && (
                <>
                  <button
                    type="button"
                    aria-label="Previous image"
                    onClick={() => step(-1)}
                    className="absolute top-1/2 left-2 -translate-y-1/2 rounded-full bg-black/60 p-1.5 text-white hover:bg-black/80"
                  >
                    <ChevronLeftIcon className="size-5" />
                  </button>
                  <button
                    type="button"
                    aria-label="Next image"
                    onClick={() => step(1)}
                    className="absolute top-1/2 right-2 -translate-y-1/2 rounded-full bg-black/60 p-1.5 text-white hover:bg-black/80"
                  >
                    <ChevronRightIcon className="size-5" />
                  </button>
                </>
              )}
            </div>
            <div className="flex items-center gap-3 px-1 text-xs text-neutral-400">
              <span className="min-w-0 truncate text-neutral-200">{img.name}</span>
              {img.note && <span className="shrink-0">{img.note}</span>}
              {many && (
                <span className="shrink-0 tabular-nums">
                  {(index ?? 0) + 1} / {images.length}
                </span>
              )}
              <span className="shrink-0">{actual ? "actual size · click to fit" : "click to see actual size"}</span>
              <a href={img.src} target="_blank" rel="noreferrer" className="ml-auto flex shrink-0 items-center gap-1 hover:text-neutral-100">
                <ExternalLinkIcon className="size-3.5" /> original
              </a>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
