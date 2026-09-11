import { useState } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { createQueryClient } from "./queryClient";
import { Dashboard } from "../monitors/Dashboard";
import { ConnectionBadge } from "./ConnectionBadge";
import { useLiveMonitors } from "./useLiveMonitors";
import { useNow } from "./useNow";
import type { LiveOptions } from "./useLiveMonitors";

/**
 * The real dashboard: fetch once, then follow the stream.
 *
 * It is the only stateful component in the feature. Everything below it stays
 * a pure function of props, which is what lets the workbench render the same
 * `Dashboard` from a demo fixture.
 */

export type LiveDashboardProps = LiveOptions & {
  /** Explicit heartbeat width; required in jsdom, which has no layout. */
  beatWidth?: number;
  compact?: boolean;
};

export function LiveDashboard({ beatWidth, compact, ...live }: LiveDashboardProps) {
  const [query, setQuery] = useState("");
  const { monitors, status, loading, error, announcement } = useLiveMonitors(live);
  const now = useNow();

  const newest = monitors.reduce<number | null>(
    (max, m) => (m.lastCheck !== null && (max === null || m.lastCheck > max) ? m.lastCheck : max),
    null,
  );

  // A failed first load is not a stale dashboard, it is an empty one. Saying
  // so plainly beats an empty state that implies "no monitors configured".
  if (error !== null && monitors.length === 0) {
    return (
      <section className="mon-dashboard">
        <p role="alert" className="mon-result-count">
          Could not load monitors: {error.message}
        </p>
      </section>
    );
  }

  if (loading && monitors.length === 0) {
    return (
      <section className="mon-dashboard">
        {/* Deliberately a sentence, not a skeleton: skeletons of unknown-length
            lists guess wrong and flash, and this request is one query. */}
        <p className="mon-result-count">Loading monitors…</p>
      </section>
    );
  }

  return (
    <Dashboard
      monitors={monitors}
      query={query}
      onQueryChange={setQuery}
      announcement={announcement}
      beatWidth={beatWidth}
      compact={compact}
      banner={<ConnectionBadge status={status} since={newest} now={now} />}
    />
  );
}

/** Mounts the live dashboard with its own query client. */
export function LiveDashboardRoot(props: LiveDashboardProps & { client?: QueryClient }) {
  const { client, ...rest } = props;
  const [fallback] = useState(createQueryClient);
  return (
    <QueryClientProvider client={client ?? fallback}>
      <LiveDashboard {...rest} />
    </QueryClientProvider>
  );
}
