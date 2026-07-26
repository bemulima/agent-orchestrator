import type { NextConfig } from "next";

const apiOrigin = process.env.ORCHESTRATOR_API_URL ?? "http://orchestrator:8080";

const config: NextConfig = {
  output: "standalone",
  turbopack: { root: process.cwd() },
  allowedDevOrigins: ["127.0.0.1", "localhost"],
  async rewrites() {
    return [{ source: "/api/v1/:path*", destination: `${apiOrigin}/api/v1/:path*` }];
  },
};

export default config;
