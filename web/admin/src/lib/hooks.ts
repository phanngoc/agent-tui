"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";

/**
 * useFetch loads a gateway path and loads it again whenever `deps` change —
 * pass a version from useVersion to follow live changes. The previous data
 * stays on screen while the next request is in flight, so a refresh caused
 * by an event never flashes the page empty.
 */
export function useFetch<T>(path: string | null, deps: unknown[] = []) {
  const [asked, setAsked] = useState(0);
  const key = JSON.stringify([path, asked, ...deps]);
  const [state, setState] = useState<{ data?: T; error: string | null; key: string }>({ error: null, key: "" });
  const seq = useRef(0);

  useEffect(() => {
    if (!path) return;
    const n = ++seq.current;
    api.get<T>(path).then(
      (d) => {
        if (n === seq.current) setState({ data: d, error: null, key });
      },
      (e: Error) => {
        if (n === seq.current) setState((s) => ({ ...s, error: e.message, key }));
      },
    );
  }, [path, key]);

  const reload = useCallback(() => setAsked((n) => n + 1), []);
  const setData = useCallback((f: (d: T | undefined) => T | undefined) => setState((s) => ({ ...s, data: f(s.data) })), []);
  // Loading until the newest request has answered.
  const loading = !!path && state.key !== key;
  return { data: state.data, error: state.error, loading, reload, setData };
}

/** useAction wraps an async call with a pending flag and an error. */
export function useAction<A extends unknown[], R>(fn: (...args: A) => Promise<R>) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const run = useCallback(
    async (...args: A): Promise<R | undefined> => {
      setPending(true);
      setError(null);
      try {
        return await fn(...args);
      } catch (e) {
        setError((e as Error).message);
        return undefined;
      } finally {
        setPending(false);
      }
    },
    [fn],
  );
  return { run, pending, error };
}
