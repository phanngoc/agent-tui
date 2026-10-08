"use client";

import { useEffect } from "react";
import { useFetch } from "./hooks";
import { onEvent } from "./store";
import type { Message, Session } from "./types";

/**
 * useLiveSession loads a session and keeps its transcript current from the
 * gateway's events: a message as it lands, a sub-agent's progress and a
 * call's result on the message that made it. A turn starting or ending
 * refetches, to settle on what is on disk.
 */
export function useLiveSession<T extends { session: Session }>(id: string | null) {
  const f = useFetch<T>(id ? `/api/sessions/${id}` : null, []);
  const { reload, setData } = f;
  useEffect(() => {
    if (!id) return;
    const edit = (fn: (msgs: Message[]) => Message[]) =>
      setData((d) =>
        d
          ? {
              ...d,
              session: { ...d.session, messages: fn(d.session.messages) },
            }
          : d,
      );
    return onEvent((e) => {
      if (e.session !== id) return;
      if (e.type === "message") {
        edit((cur) => {
          const msgs = [...cur];
          msgs[e.data.index] = e.data.message;
          return msgs;
        });
      }
      if (e.type === "subagent") {
        // A sub-agent's progress belongs on the Agent call that started it.
        const { tool_use, agent } = e.data;
        edit((msgs) =>
          msgs.map((m) =>
            m.tools?.some((t) => t.id === tool_use)
              ? {
                  ...m,
                  tools: m.tools.map((t) => (t.id === tool_use ? { ...t, agent } : t)),
                }
              : m,
          ),
        );
      }
      if (e.type === "tool.done") {
        // A finished call's result belongs on the message that made it.
        const call = e.data.call;
        edit((msgs) =>
          msgs.map((m) =>
            m.tools?.some((t) => t.id === call.id)
              ? {
                  ...m,
                  tools: m.tools.map((t) =>
                    t.id === call.id
                      ? {
                          ...t,
                          ...call,
                          name: call.name || t.name,
                          input: call.input ?? t.input,
                        }
                      : t,
                  ),
                }
              : m,
          ),
        );
      }
      if (e.type === "turn.done" || e.type === "turn.started") setTimeout(() => void reload(), 400);
    });
  }, [id, reload, setData]);
  return f;
}
