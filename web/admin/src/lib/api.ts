// The gateway's address. Served by the gateway itself, the admin talks to
// its own origin; run from `next dev`, it talks to the default gateway port
// unless NEXT_PUBLIC_GATEWAY says otherwise.
export function gatewayBase(): string {
  if (typeof window === "undefined") return "";
  const env = process.env.NEXT_PUBLIC_GATEWAY;
  if (env) return env.replace(/\/$/, "");
  const { port, protocol, hostname } = window.location;
  if (port === "3000" || port === "3001") return `${protocol}//127.0.0.1:7788`;
  return `${protocol}//${hostname}${port ? ":" + port : ""}`;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(gatewayBase() + path, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let data: unknown = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = text;
  }
  if (!res.ok) {
    const msg = (data && typeof data === "object" && "error" in data ? (data as { error: string }).error : text) || res.statusText;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export const api = {
  get: <T,>(path: string) => request<T>("GET", path),
  post: <T,>(path: string, body?: unknown) => request<T>("POST", path, body ?? {}),
  put: <T,>(path: string, body: unknown) => request<T>("PUT", path, body),
  del: <T,>(path: string) => request<T>("DELETE", path),
};

export function qs(params: Record<string, string | number | undefined | null>): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") p.set(k, String(v));
  }
  const s = p.toString();
  return s ? "?" + s : "";
}
