"use client";

import * as React from "react";

// Following the answer down, until you scroll away — as the TUI does
// (internal/ui/follow.go).
//
// A streaming answer used to pull the transcript to its bottom on every delta,
// which undid a scroll up to reread something a few times a second for as
// long as the turn lasted. The reader's hand on the wheel is the stronger
// signal, so it wins: the transcript follows the bottom only while you are at
// it. Scroll up and it stays where you put it, with a button saying there is
// more below. Scroll back to the bottom, press End, use the button or send a
// prompt, and it follows again.

/** How close to the bottom still counts as at it, in pixels. */
const SLACK = 4;
/** How long after a wheel, touch or key a scroll counts as the reader's own. */
const INTENT_MS = 800;

function atBottom(el: HTMLElement) {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= SLACK;
}

/**
 * useFollow keeps a scroller at its bottom while its content grows, unless the
 * reader has scrolled away. ready says the scroller is mounted: pass false
 * while a loading state stands in for it.
 */
export function useFollow(ready: boolean) {
  const scroller = React.useRef<HTMLDivElement>(null);
  const content = React.useRef<HTMLDivElement>(null);
  const away = React.useRef(false);
  const [below, setBelow] = React.useState(false);

  const setAway = React.useCallback((v: boolean) => {
    away.current = v;
    setBelow(v);
  }, []);

  /** toBottom goes to the bottom and follows from there: for what the reader does on purpose. */
  const toBottom = React.useCallback(() => {
    setAway(false);
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [setAway]);

  React.useEffect(() => {
    const el = scroller.current;
    if (!ready || !el) return;
    // Programmatic scrolls always land on the bottom, so only a scroll that
    // follows the reader's own input can take the transcript away from it.
    // Layout shifts (a diagram rendering, a tool result arriving) move the
    // content, not the reader.
    let intent = 0;
    const mark = () => {
      intent = performance.now();
    };
    const onWheel = (e: WheelEvent) => {
      mark();
      // Up is away at once, before the next delta can pin it back.
      if (e.deltaY < 0 && el.scrollTop > 0) setAway(true);
    };
    const onScroll = () => {
      if (atBottom(el)) setAway(false);
      else if (performance.now() - intent < INTENT_MS) setAway(true);
    };
    // The bottom moves when the content grows or the pane resizes; follow it.
    const pin = () => {
      if (!away.current) el.scrollTop = el.scrollHeight;
    };
    const ro = new ResizeObserver(pin);
    if (content.current) ro.observe(content.current);
    ro.observe(el);
    pin();

    const opts = { passive: true } as const;
    el.addEventListener("wheel", onWheel, opts);
    el.addEventListener("touchstart", mark, opts);
    el.addEventListener("touchmove", mark, opts);
    el.addEventListener("pointerdown", mark, opts);
    el.addEventListener("keydown", mark);
    el.addEventListener("scroll", onScroll, opts);
    return () => {
      ro.disconnect();
      el.removeEventListener("wheel", onWheel);
      el.removeEventListener("touchstart", mark);
      el.removeEventListener("touchmove", mark);
      el.removeEventListener("pointerdown", mark);
      el.removeEventListener("keydown", mark);
      el.removeEventListener("scroll", onScroll);
    };
  }, [ready, setAway]);

  return { scroller, content, below, toBottom };
}
