// StreamSource reads a server-sent event stream the way EventSource does,
// but by POST.
//
// A Cloudflare quick tunnel holds back the body of a streamed GET until the
// response ends (cloudflared#1449), and EventSource only ever GETs, so through
// one the event stream arrived all at once, never. A POST streams. The
// gateway answers its streams to either method.
//
// It does not reconnect by itself: when the stream ends or fails it is
// CLOSED and onerror is told, and the caller opens a new one.

export type StreamEvent = { type: string; data: string; lastEventId: string };
type Listener = (e: StreamEvent) => void;

export class StreamSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;

  readyState = StreamSource.CONNECTING;
  onopen: (() => void) | null = null;
  onmessage: Listener | null = null;
  onerror: (() => void) | null = null;

  private listeners = new Map<string, Set<Listener>>();
  private ctl = new AbortController();

  constructor(url: string) {
    void this.run(url);
  }

  addEventListener(type: string, fn: Listener) {
    let set = this.listeners.get(type);
    if (!set) this.listeners.set(type, (set = new Set()));
    set.add(fn);
  }

  close() {
    this.readyState = StreamSource.CLOSED;
    this.ctl.abort();
  }

  private dispatch(e: StreamEvent) {
    if (this.readyState === StreamSource.CLOSED) return;
    if (e.type === "message") this.onmessage?.(e);
    this.listeners.get(e.type)?.forEach((fn) => fn(e));
  }

  private async run(url: string) {
    try {
      const res = await fetch(url, {
        method: "POST",
        headers: { Accept: "text/event-stream" },
        cache: "no-store",
        signal: this.ctl.signal,
      });
      if (!res.ok || !res.body) throw new Error(`stream: ${res.status}`);
      if (this.readyState === StreamSource.CLOSED) return;
      this.readyState = StreamSource.OPEN;
      this.onopen?.();
      const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
      let buf = "";
      let type = "message";
      let data: string[] = [];
      let id = "";
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += value;
        let nl: number;
        while ((nl = buf.indexOf("\n")) >= 0) {
          let line = buf.slice(0, nl);
          buf = buf.slice(nl + 1);
          if (line.endsWith("\r")) line = line.slice(0, -1);
          if (line === "") {
            if (data.length) this.dispatch({ type, data: data.join("\n"), lastEventId: id });
            type = "message";
            data = [];
            continue;
          }
          if (line.startsWith(":")) continue; // a comment: the keep-alive
          const colon = line.indexOf(":");
          const field = colon < 0 ? line : line.slice(0, colon);
          let val = colon < 0 ? "" : line.slice(colon + 1);
          if (val.startsWith(" ")) val = val.slice(1);
          if (field === "event") type = val;
          else if (field === "data") data.push(val);
          else if (field === "id") id = val;
        }
      }
    } catch {
      // ended below
    }
    if (this.readyState !== StreamSource.CLOSED) {
      this.readyState = StreamSource.CLOSED;
      this.onerror?.();
    }
  }
}
