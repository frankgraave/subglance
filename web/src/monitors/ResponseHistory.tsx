import { useState, type ReactNode } from "react";
import { causeWords } from "../incidents/story";
import { Card } from "../components/Card";
import { IconResponse } from "../components/icons";
import { formatMoment } from "./detail";
import { toUnixMs } from "./types";
import { failureRuns, type ResponseHeartbeat } from "./responseHistory";

export type ResponseHistoryProps = {
  heartbeats: readonly ResponseHeartbeat[];
  loading?: boolean;
  error?: Error | null;
};

// Defence in depth: the checker already stores only this allowlist. Never turn
// a future unfiltered API field into a credential disclosure in the UI.
const HEADERS = new Set([
  "content-type", "content-length", "server", "date", "retry-after",
  "location", "x-request-id", "x-correlation-id", "cf-ray",
]);

/** Historical diagnostics only: current monitor state cannot explain old rows. */
export function ResponseHistory({ heartbeats, loading = false, error = null }: ResponseHistoryProps) {
  // The runs carry their keys from the previous history, so a run keeps its
  // row while its members change at both ends (see failureRuns). Stored as
  // state and replaced during render when the history changes: React's
  // pattern for information from the previous render, the one LatencyChart
  // uses for its window.
  const [grouped, setGrouped] = useState(() => ({ source: heartbeats, ...failureRuns(heartbeats) }));
  let current = grouped;
  if (grouped.source !== heartbeats) {
    current = { source: heartbeats, ...failureRuns(heartbeats, grouped.keys) };
    setGrouped(current);
  }
  const { runs } = current;
  return <Card title="Failure responses" icon={<IconResponse />} headingLevel={2}>
    <div className="response-history">
      {loading ? <p role="status">Loading failure responses…</p> : null}
      {error ? <p role="alert">Could not load failure responses: {error.message}. Previously loaded history may be out of date.</p> : null}
      {/* The same empty-state face as every other card on the page: "nothing
          failed" is a finding, and at body size beside the incidents card's
          headline it read as a different kind of statement. */}
      {!loading && !error && runs.length === 0 ? <p className="mon-detail-empty">No failed checks in the recent history.</p> : null}
      {runs.length > 0 ? <ul className="response-history-list">
        {runs.map((run) => <FailureRunItem key={run.key} beats={run.beats} />)}
      </ul> : null}
    </div>
  </Card>;
}

/** What a failed check was, in the order the row prints it. */
function FailureFacts({ hb }: { hb: ResponseHeartbeat }) {
  return <>
    <span>{hb.assessment === "warning" ? "Warning — unconfirmed failure; no alert; excluded from uptime" : hb.assessment === "down" ? "Down — confirmed failure" : "Failed check — confirmation not recorded"}</span>
    {hb.maintenance ? <span>Maintenance — alerts suppressed; excluded from uptime</span> : null}
    {hb.failure_kind ? <span>{causeWords(hb.failure_kind)}</span> : null}
    {hb.status_code ? <span>HTTP {hb.status_code}</span> : null}
  </>;
}

/** One stored response: truncation stated outside, body behind a disclosure. */
function CapturedResponse({ response, label }: { response: NonNullable<ResponseHeartbeat["response"]>; label: ReactNode }) {
  return <>
    {response.truncated ? <p>Truncated — only the beginning of the response was captured.</p> : null}
    <details className="response-history-disclosure">
      <summary>{label}</summary>
      <dl className="response-history-headers face-mono">
        {Object.entries(response.headers ?? {})
          .filter(([name]) => HEADERS.has(name.toLowerCase()))
          .map(([name, value]) => <div key={name}><dt>{name}</dt><dd>{value}</dd></div>)}
      </dl>
      {/* Endpoint content stays a React text child. No markup interpreter. */}
      {response.body === "" ? <p>The captured response body was empty.</p> : (
        // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- A named scroll region needs keyboard focus so its full text can be read without a pointer.
        <pre className="face-mono" role="region" aria-label="Captured response body" tabIndex={0}>{response.body}</pre>
      )}
    </details>
  </>;
}

/**
 * A run of checks that failed the same way, told once (SUB-184).
 *
 * The facts and the error are printed once; a run of more than one adds its
 * count and the span it covers. What differs per check stays per check: each
 * stored response keeps its own disclosure, keyed by its heartbeat ID, and the
 * checks without one are counted by their recorded reason instead of repeating
 * one sentence forty times.
 *
 * One component for one check and for forty, on purpose. When an identical
 * failure arrives, a lone row becomes a run; were those two components, the
 * row would remount and close the response somebody had open. Here the
 * disclosures sit in the same slot either way, so they keep their element.
 */
function FailureRunItem({ beats }: { beats: readonly ResponseHeartbeat[] }) {
  const newest = beats[0];
  const oldest = beats[beats.length - 1];
  const single = beats.length === 1;
  const missing = new Map<NonNullable<ResponseHeartbeat["response_capture_reason"]> | "unknown", number>();
  for (const hb of beats) {
    if (hb.response) continue;
    const reason = hb.response_capture_reason ?? "unknown";
    missing.set(reason, (missing.get(reason) ?? 0) + 1);
  }
  return <li className="response-history-beat" data-count={beats.length}>
    <div className="response-history-check">
      {single
        ? <time className="face-mono" dateTime={newest.ts}>{formatMoment(toUnixMs(newest.ts))}</time>
        : <span className="face-mono">
          <time dateTime={oldest.ts}>{formatMoment(toUnixMs(oldest.ts))}</time>
          {" – "}
          <time dateTime={newest.ts}>{formatSpanEnd(toUnixMs(oldest.ts), toUnixMs(newest.ts))}</time>
        </span>}
      {single ? null : <strong className="response-history-count">{beats.length} checks in a row</strong>}
      <FailureFacts hb={newest} />
    </div>
    {newest.error ? <p>{newest.error}</p> : null}
    {beats.map((hb) => hb.response
      ? <CapturedResponse
          key={hb.id}
          response={hb.response}
          label={single ? "Captured response" : <>Captured response · <time dateTime={hb.ts}>{formatMoment(toUnixMs(hb.ts))}</time></>}
        />
      : null)}
    {[...missing].map(([reason, count]) => <p key={reason}>
      {single ? captureReasonText(newest.response_capture_reason) : captureReasonCount(reason, count)}
    </p>)}
  </li>;
}

/**
 * The end of a run's span: just the time when it ends on the day it began,
 * because repeating the date makes the line twice as long to say nothing.
 */
function formatSpanEnd(start: number | null, end: number | null): string | null {
  if (start === null || end === null) return formatMoment(end);
  const a = new Date(start);
  const b = new Date(end);
  if (a.toDateString() !== b.toDateString()) return formatMoment(end);
  return b.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

function captureReasonText(reason: ResponseHeartbeat["response_capture_reason"]): string {
  switch (reason) {
    case "disabled": return "Capture was switched off for this check.";
    case "flapping": return "Capture stopped while this monitor was flapping; this check's response was not stored.";
    case "budget": return "This incident's response capture allowance had been used; this check's response was not stored.";
    default: return "No captured response. Reason not recorded.";
  }
}

/** The same four decisions, said once for every check in a run that shares one. */
function captureReasonCount(reason: NonNullable<ResponseHeartbeat["response_capture_reason"]> | "unknown", count: number): string {
  const checks = count === 1 ? "1 of these checks" : `${count} of these checks`;
  switch (reason) {
    case "disabled": return `Capture was switched off for ${checks}.`;
    case "flapping": return `Capture stopped while this monitor was flapping; no response was stored for ${checks}.`;
    case "budget": return `This incident's response capture allowance had been used; no response was stored for ${checks}.`;
    default: return `No captured response for ${checks}. Reason not recorded.`;
  }
}
