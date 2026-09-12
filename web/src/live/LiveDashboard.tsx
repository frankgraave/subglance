import { useState } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { createQueryClient } from "./queryClient";
import { Dashboard } from "../monitors/Dashboard";
import { ConnectionBadge } from "./ConnectionBadge";
import { useLiveMonitors } from "./useLiveMonitors";
import { useNow } from "./useNow";
import { StatusWall } from "../wall/StatusWall";
import type { LayoutId } from "../shell/preferences";
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
  /** The user's layout setting, already vetoed by the viewport if need be. */
  layout?: LayoutId;
  /** Shown on the status wall's header line. */
  instance?: string;
  /** Leaves the status wall. Provided by the shell, which owns the setting. */
  onExitWall?: () => void;
};

export function LiveDashboard({
  beatWidth,
  layout,
  instance,
  onExitWall,
  ...live
}: LiveDashboardProps) {
  const [query, setQuery] = useState("");
  const { monitors, status, loading, error, announcement, reconnect } = useLiveMonitors(live);
  const now = useNow();

  const newest = monitors.reduce<number | null>(
    (max, m) => (m.lastCheck !== null && (max === null || m.lastCheck > max) ? m.lastCheck : max),
    null,
  );

  const empty = monitors.length === 0;
  // A failed first load is not a stale dashboard, it is an empty one. Saying
  // so plainly beats an empty state that implies "no monitors configured".
  // Deliberately a sentence and not a skeleton: skeletons of unknown-length
  // lists guess wrong and flash, and this request is one query.
  const notice =
    error !== null && empty
      ? `Could not load monitors: ${error.message}`
      : loading && empty
        ? "Loading monitors…"
        : undefined;

  // The wall replaces the whole page rather than sitting inside it: hiding the
  // sidebar and the topbar is the entire reason the layout exists (§7). It
  // also carries its own stale signal, because there is no chrome to hold a
  // banner — the canvas takes a warm border instead.
  //
  // It is checked *before* the loading and error branches on purpose. Those
  // render the dashboard's chrome, which the wall does not have — falling into
  // them would strand whoever selected the wall on a bare sentence with no
  // header, no clock and, worse, no visible way back out. The wall renders its
  // own frame and carries the sentence inside it instead.
  if (layout === "wall") {
    return (
      <StatusWall
        monitors={monitors}
        instance={instance}
        stale={status === "offline"}
        onExit={onExitWall}
        notice={notice}

      />
    );
  }

  if (notice !== undefined) {
    return (
      <section className="mon-dashboard">
        <p role={error !== null ? "alert" : undefined} className="mon-result-count">
          {notice}
        </p>
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
      layout={layout}
      stale={status === "offline"}
      banner={
        <ConnectionBadge status={status} since={newest} now={now} onReconnect={reconnect} />
      }
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
