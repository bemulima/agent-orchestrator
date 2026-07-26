"use client";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { LiveUpdates } from "@/components/live-updates";

export function Providers({ children }: { children: React.ReactNode }) {
  const [client] = useState(() => new QueryClient({
    defaultOptions: { queries: { staleTime: 5_000, refetchInterval: 10_000, retry: 1 } },
  }));
  return <QueryClientProvider client={client}><LiveUpdates />{children}</QueryClientProvider>;
}
