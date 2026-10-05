import { useQuery } from "@tanstack/react-query";
import { Card, Panel } from "../components/Card";
import { IconPulse } from "../components/icons";
import { useNow } from "../live/useNow";
import { formatMomentIso } from "../format/format";
import { fetchWatchdog, watchdogKey, type WatchdogState } from "./api";

function useWatchdog() {
  const query = useQuery({ queryKey: watchdogKey, queryFn: ({ signal }) => fetchWatchdog(signal), refetchInterval: 15_000 });
  const now = useNow();
  return { ...query, old: query.data !== undefined && (query.isError || now - query.dataUpdatedAt > 45_000) };
}

function PingTime({ value }: { value: string | null }) {
  return value === null ? <>None since this process started</> : <time dateTime={value}>{formatMomentIso(value)}</time>;
}

function resultText(data: WatchdogState): string {
  switch (data.last_result) {
    case "succeeded": return `Last ping succeeded (HTTP ${data.last_status_code}).`;
    case "rejected": return `Last ping rejected (HTTP ${data.last_status_code}).`;
    case "transport_error": return "Last ping could not reach the receiver.";
    case "timeout": return "Last ping timed out.";
    case "canceled": return "Last ping was canceled.";
    case "request_error": return "Last ping could not be sent.";
    case null: return "Waiting for the first ping; startup does not count as success.";
  }
}

// The dedicated README section explains the limit and links to configuration.
const watchdogReadme = "https://github.com/frankgraave/subglance/blob/develop/README.md#self-monitoring";

export function WatchdogNotice() {
  const query = useWatchdog();
  if (!query.data || query.data.configured || query.old) return null;
  return <p className="watchdog-notice">No watchdog is configured: SubGlance cannot report its own outage. {" "}
    <a href={watchdogReadme}>Read about self-monitoring</a>.
  </p>;
}

export function WatchdogCard() {
  const query = useWatchdog();
  const data = query.data;
  return <Card title="Self-monitoring" icon={<IconPulse />}>
    <Panel>
      <p className="panel-lead">Watchdog</p>
      {!data ? <p>{query.isError ? "Watchdog state unavailable." : "Loading watchdog state…"}</p> : <>
        {query.old && <p role="status">Watchdog state unavailable. Showing the last retrieved history.</p>}
        <p>{data.configured ? "Configured" : "Not configured"}</p>
        {/* The card is where somebody looks for the switch, so the unset state
            says what a watchdog is and what to set, and links where the
            dashboard notice does: one place to read about it, not two. */}
        {!data.configured && <p className="panel-note">
          A watchdog is an outside service that SubGlance pings on a schedule and that raises the alarm when the pings stop: set{" "}
          <code>SUBGLANCE_WATCHDOG_URL</code> to its ping address and restart SubGlance. <a href={watchdogReadme}>Read about self-monitoring</a>.
        </p>}
        {data.configured && <>
          {data.overdue && <p>Watchdog activity is overdue. Past pings do not confirm current operation.</p>}
          {data.suppressed && <p>Pings withheld: the checking pipeline has not progressed.</p>}
          {data.in_flight ? <p>Ping in progress; the outcome is not known yet.</p> : <p>{resultText(data)}</p>}
          {data.last_event === "stopped" && <p>The latest attempt was a clean-shutdown ping.</p>}
          <dl className="panel-facts">
            <div><dt>Last successful ping</dt><dd><PingTime value={data.last_success_at} /></dd></div>
            <div><dt>Last attempt</dt><dd><PingTime value={data.last_attempt_at} /></dd></div>
          </dl>
          <p className="panel-note">History is held only for this process. A successful ping does not confirm that the receiver will raise an alarm.</p>
        </>}
      </>}
    </Panel>
  </Card>;
}
