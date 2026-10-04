# agent-tui admin

The web side of agent-tui: sessions live as they stream, a chat box, and the
agent's memory, skills, MCP servers and settings — global and per project.
Next.js (static export) + shadcn/ui + Tailwind + Zustand. All data comes from
the gateway, `agent-tui serve`, at runtime.

```sh
npm install
npm run dev     # http://localhost:3000, against a gateway on 127.0.0.1:7788
npm run build   # static export into out/, which `agent-tui serve` serves
```

`NEXT_PUBLIC_GATEWAY` points the dev server at another gateway address.

- `src/lib/store.ts` — one SSE connection to the gateway; live turn state and
  "versions" pages refetch on.
- `src/lib/api.ts`, `src/lib/types.ts` — the gateway's REST API and its JSON.
- `src/app/*` — Overview, Sessions (transcript, composer, context trace),
  Memory (records, scenes & persona, learning activity, import), Skills, MCP
  servers, Settings (with a context preview).

See `docs/gateway-admin-và-bộ-nhớ.md` in the repository for the architecture.
