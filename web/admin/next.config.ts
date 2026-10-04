import type { NextConfig } from "next";

// The admin is a static export: `agent-tui serve` serves web/admin/out (or an
// `admin` folder next to the binary) and everything dynamic comes from the
// gateway's API at runtime. In `next dev` the pages talk to the gateway on
// 127.0.0.1:7788 directly (see src/lib/api.ts).
const nextConfig: NextConfig = {
  output: "export",
  images: { unoptimized: true },
};

export default nextConfig;
