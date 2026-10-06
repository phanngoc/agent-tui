"use client";

import { create } from "zustand";
import { api, gatewayBase } from "./api";
import { StreamSource } from "./sse";
import type { GatewayEvent, Live, Peer, Summary } from "./types";

// One connection to the gateway's event stream per tab. Every page reads the
// live state from here and refetches what it shows when a "version" it
// depends on moves, so a change made anywhere — the terminal, another tab,
// the learner — appears without a reload.

type Listener = (e: GatewayEvent) => void;
const listeners = new Set<Listener>();

/** onEvent subscribes to every gateway event; it returns the unsubscribe. */
export function onEvent(fn: Listener): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export type Kind = "sessions" | "memory" | "skills" | "mcp" | "settings" | "learn" | "peers" | "schedule" | "remote";

interface State {
  root: string; // the project being looked at; "" means global only
  setRoot: (root: string) => void;
  connected: boolean;
  seq: number;
  live: Record<string, Live>;
  summaries: Record<string, Summary>;
  peers: Peer[];
  versions: Record<Kind, number>;
  feed: GatewayEvent[];
  bump: (k: Kind) => void;
  connect: () => void;
}

const emptyLive = (session: string, owner = ""): Live => ({
  session,
  owner,
  busy: false,
  running: {},
  output: {},
  approvals: {},
  choices: {},
});

/** applyLive mirrors the hub's own bookkeeping, event by event. */
function applyLive(prev: Record<string, Live>, e: GatewayEvent): Record<string, Live> {
  const id = e.session;
  if (!id) return prev;
  const d = e.data ?? {};
  if (e.type === "session.deleted") {
    const next = { ...prev };
    delete next[id];
    return next;
  }
  if (e.type === "turn.started") {
    return {
      ...prev,
      [id]: { ...emptyLive(id, e.origin), busy: true, status: "thinking", prompt: d.prompt, engine: d.engine, started: e.at },
    };
  }
  const cur = prev[id];
  if (!cur) return prev;
  const l: Live = { ...cur };
  switch (e.type) {
    case "text.delta":
      l.partial = (l.partial ?? "") + d.text;
      break;
    case "thinking.delta":
      l.thinking = ((l.thinking ?? "") + d.text).slice(-20000);
      break;
    case "status":
      l.status = d.text;
      break;
    case "message":
      l.partial = "";
      l.thinking = "";
      break;
    case "tool.start":
      l.running = { ...l.running, [d.call.id]: d.call };
      break;
    case "tool.output":
      l.output = { ...l.output, [d.id]: ((l.output[d.id] ?? "") + d.text).slice(-32000) };
      break;
    case "tool.done": {
      const running = { ...l.running };
      delete running[d.call.id];
      const output = { ...l.output };
      delete output[d.call.id];
      l.running = running;
      l.output = output;
      break;
    }
    case "approval.request":
      l.approvals = { ...l.approvals, [d.id]: d };
      break;
    case "approval.resolved": {
      const a = { ...l.approvals };
      delete a[d.id];
      l.approvals = a;
      break;
    }
    case "choice.request":
      l.choices = { ...l.choices, [d.id]: d };
      break;
    case "choice.resolved": {
      const c = { ...l.choices };
      delete c[d.id];
      l.choices = c;
      break;
    }
    case "subagent":
      l.agents = { ...(l.agents ?? {}), [d.tool_use]: d.agent };
      break;
    case "turn.done":
      // The turn's agents stay on the map until the next turn starts.
      return { ...prev, [id]: { ...emptyLive(id, cur.owner), error: d.error, agents: cur.agents } };
    default:
      return prev;
  }
  return { ...prev, [id]: l };
}

function storedRoot(): string {
  try {
    return localStorage.getItem("agent-tui.root") ?? "";
  } catch {
    return "";
  }
}

let source: StreamSource | null = null;

export const useGateway = create<State>((set, get) => ({
  root: "",
  setRoot: (root) => {
    try {
      localStorage.setItem("agent-tui.root", root);
    } catch {}
    set({ root });
  },
  connected: false,
  seq: 0,
  live: {},
  summaries: {},
  peers: [],
  versions: { sessions: 0, memory: 0, skills: 0, mcp: 0, settings: 0, learn: 0, peers: 0, schedule: 0, remote: 0 },
  feed: [],
  bump: (k) => set((s) => ({ versions: { ...s.versions, [k]: s.versions[k] + 1 } })),

  connect: () => {
    if (source || typeof window === "undefined") return;
    if (!get().root) set({ root: storedRoot() });

    const snapshot = async () => {
      try {
        const [live, peers] = await Promise.all([
          api.get<Record<string, Live>>("/api/live"),
          api.get<Peer[]>("/api/gateway/peers"),
        ]);
        set({ live, peers });
      } catch {}
    };

    const open = () => {
      const after = get().seq;
      source = new StreamSource(gatewayBase() + "/api/events" + (after ? `?after=${after}` : ""));
      source.addEventListener("hello", () => {
        set({ connected: true });
        void snapshot();
        get().bump("sessions");
      });
      source.onmessage = (msg) => {
        let e: GatewayEvent;
        try {
          e = JSON.parse(msg.data);
        } catch {
          return;
        }
        set((s) => {
          const patch: Partial<State> = { seq: e.seq, live: applyLive(s.live, e) };
          if (e.type === "session.updated" && e.data) {
            patch.summaries = { ...s.summaries, [e.data.id]: e.data };
          }
          if (e.type !== "text.delta" && e.type !== "thinking.delta" && e.type !== "tool.output") {
            patch.feed = [e, ...s.feed].slice(0, 300);
          }
          return patch;
        });
        switch (e.type) {
          case "config.changed": {
            const kind = e.data?.kind as string;
            const map: Record<string, Kind> = { skills: "skills", mcp: "mcp", memory: "memory", settings: "settings", skill: "skills", schedule: "schedule", remote: "remote", telegram: "remote" };
            if (map[kind]) get().bump(map[kind]);
            break;
          }
          case "learn":
            get().bump("learn");
            get().bump("memory");
            if (e.data?.stage === "skill") get().bump("skills");
            break;
          case "peer.joined":
          case "peer.left":
            void snapshot();
            get().bump("peers");
            break;
          case "session.updated":
          case "turn.done":
            get().bump("sessions");
            break;
          case "schedule.run":
            get().bump("schedule");
            break;
        }
        listeners.forEach((fn) => fn(e));
      };
      source.onerror = () => {
        set({ connected: false });
        // The stream does not retry on its own; if the gateway restarted,
        // its sequence numbers did too, and the hello starts over from the
        // snapshot.
        source = null;
        setTimeout(open, 2000);
      };
    };
    open();
  },
}));

/** useVersion returns a number that changes when any of the kinds does. */
export function useVersion(...kinds: Kind[]): string {
  return useGateway((s) => kinds.map((k) => s.versions[k]).join("."));
}
