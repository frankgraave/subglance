import { Card } from "../components/Card";
import { IconAlert } from "../components/icons";
import { formatMoment } from "./detail";
import { toUnixMs } from "./types";
import type { ResponseHeartbeat } from "./responseHistory";

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
  const failures = heartbeats.filter((hb) => !hb.ok);
  return <Card title="Failure responses" icon={<IconAlert />} headingLevel={2}>
    <div className="response-history">
      {loading ? <p role="status">Loading failure responses…</p> : null}
      {error ? <p role="alert">Could not load failure responses: {error.message}. Previously loaded history may be out of date.</p> : null}
      {!loading && !error && failures.length === 0 ? <p>No failed checks in the recent history.</p> : null}
      {failures.length > 0 ? <ul className="response-history-list">
        {failures.map((hb, index) => <li className="response-history-beat" key={`${hb.ts}-${index}`}>
          <div className="response-history-check">
            <time className="face-mono" dateTime={hb.ts}>{formatMoment(toUnixMs(hb.ts))}</time>
            <span>Failed check</span>
            {hb.status_code ? <span>HTTP {hb.status_code}</span> : null}
          </div>
          {hb.error ? <p>{hb.error}</p> : null}
          {hb.response ? <>
            {hb.response.truncated ? <p>Truncated — only the beginning of the response was captured.</p> : null}
            <details className="response-history-disclosure">
              <summary>Captured response</summary>
              <dl className="response-history-headers face-mono">
                {Object.entries(hb.response.headers ?? {})
                  .filter(([name]) => HEADERS.has(name.toLowerCase()))
                  .map(([name, value]) => <div key={name}><dt>{name}</dt><dd>{value}</dd></div>)}
              </dl>
              {/* Endpoint content stays a React text child. No markup interpreter. */}
              {hb.response.body === "" ? <p>The captured response body was empty.</p> : (
                // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- A named scroll region needs keyboard focus so its full text can be read without a pointer.
                <pre className="face-mono" role="region" aria-label="Captured response body" tabIndex={0}>{hb.response.body}</pre>
              )}
            </details>
          </> : <p>{captureReasonText(hb.response_capture_reason)}</p>}
        </li>)}
      </ul> : null}
    </div>
  </Card>;
}

function captureReasonText(reason: ResponseHeartbeat["response_capture_reason"]): string {
  switch (reason) {
    case "disabled": return "Capture was switched off for this check.";
    case "flapping": return "Capture stopped while this monitor was flapping; this check's response was not stored.";
    case "budget": return "This incident's response capture allowance had been used; this check's response was not stored.";
    default: return "No captured response. Reason not recorded.";
  }
}
