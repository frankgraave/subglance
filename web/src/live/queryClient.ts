import { QueryClient } from "@tanstack/react-query";

/**
 * A QueryClient for the app.
 *
 * `retry: 1` rather than the default three: this UI is served by the very
 * process it is asking about, so a failed request usually means the server is
 * gone, and hammering it adds nothing. The stream, not a retry loop, is what
 * brings the page back.
 */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: 1, refetchOnWindowFocus: false, staleTime: Infinity },
    },
  });
}
