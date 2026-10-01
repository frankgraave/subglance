import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Panel } from "../components/Card";
import { ApiError } from "../api/http";
import { previewRetention, retentionKey, startRetentionAction, type CompactPlan, type Retention, type RetentionPass, type SizeCap } from "./api";
import { formatBytes } from "./format";

const DAY_MS = 86_400_000;
const count = new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 });

function When({ value }: { value: string }) {
  return <time dateTime={value}>{new Date(value).toLocaleString(undefined, { timeZoneName: "short" })}</time>;
}

function plural(n: number, one: string, many: string) {
  return `${count.format(n)} ${n === 1 ? one : many}`;
}

/** A length of time as a person would say it: 0.4 s, 12 s, 3 min. */
function formatDuration(ms: number): string {
  if (ms < 10_000) return `${(ms / 1000).toFixed(1)} s`;
  if (ms < 120_000) return `${Math.round(ms / 1000)} s`;
  return `${Math.round(ms / 60_000)} min`;
}

const TRIGGERS: Record<RetentionPass["trigger"], string> = {
  schedule: "At its time of day",
  startup: "At startup, after being missed",
  manual: "Started by hand",
};

/** What the windows removed, in one sentence. */
function removed(pass: RetentionPass): string {
  const parts = [
    plural(pass.hourly_buckets, "hourly summary", "hourly summaries"),
    plural(pass.incidents, "resolved incident", "resolved incidents"),
    plural(pass.deliveries, "delivered notification", "delivered notifications"),
  ];
  return `Folded ${plural(pass.heartbeats, "raw heartbeat", "raw heartbeats")} into hourly summaries and deleted ${parts[0]}, ${parts[1]} and ${parts[2]}.`;
}

/**
 * What the size limit did beyond the windows, or null when it did nothing.
 * Said in days of raw data, the unit the window above it is set in, so the
 * notice reads as "the limit overrode that field", which is what happened.
 */
function capNotice(cap: SizeCap | null, startedAt: string): string | null {
  if (!cap || (cap.heartbeats === 0 && cap.hourly_buckets === 0 && !cap.at_floor)) return null;
  const limit = formatBytes(cap.limit_bytes);
  const days = (since: string) => Math.max(1, Math.round((Date.parse(startedAt) - Date.parse(since)) / DAY_MS));
  const parts: string[] = [];
  if (cap.raw_since) parts.push(`Shortened to ${plural(days(cap.raw_since), "day", "days")} of raw data to stay under ${limit}.`);
  if (cap.hourly_since) parts.push(`Hourly summaries now start ${new Date(cap.hourly_since).toLocaleDateString()}.`);
  if (parts.length === 0) parts.push(`Removed history beyond the windows to stay under ${limit}.`);
  if (cap.at_floor) parts.push(`Still over ${limit} (${formatBytes(cap.after_bytes)}): only the last day and the incidents are left, and the limit never removes those.`);
  return parts.join(" ");
}

function LastPass({ pass }: { pass: RetentionPass }) {
  const notice = capNotice(pass.size_cap, pass.started_at);
  return <>
    {pass.error !== null
      ? <p className="warn-note">The last pass failed: {pass.error}</p>
      : <p>{removed(pass)}</p>}
    {notice && <p>{notice}</p>}
    <dl className="panel-facts">
      <div><dt>Started</dt><dd><When value={pass.started_at} /></dd></div>
      <div><dt>Took</dt><dd className="face-mono">{formatDuration(pass.duration_ms)}</dd></div>
      <div><dt>Freed</dt><dd className="face-mono">{formatBytes(pass.freed_bytes)}</dd></div>
      <div><dt>Trigger</dt><dd>{TRIGGERS[pass.trigger]}</dd></div>
    </dl>
  </>;
}

/*
 * Failures here wear the caveat style (a warn rail, `--ink` text), not the
 * form's `.field-error`: its `--down` text measures under 4.5:1 on a card
 * panel (Retention.browser.test.ts). A failed pass is a caveat the reader
 * must not skim past, which is what that style is for.
 */

/** Why a request to start something was refused, in words the card can show. */
function refusal(error: unknown): string {
  return error instanceof ApiError ? error.message : "Could not reach SubGlance. Check your connection and try again.";
}

/**
 * "Run now": the pass with the settings in force, shown as a count first.
 *
 * Two steps, because the pass deletes: the first press asks the preview what
 * the windows in force would remove today, and only the second starts it.
 * The size limit's share is said to be uncountable rather than left out,
 * because it depends on how much space the windows free first.
 */
function RunNow({ data }: { data: Retention }) {
  const client = useQueryClient();
  const [asked, setAsked] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const preview = useQuery({
    queryKey: ["retention-preview", data.raw.seconds, data.rollup.seconds],
    queryFn: ({ signal }) => previewRetention(data.raw.seconds, data.rollup.seconds, signal),
    enabled: asked,
    retry: false,
  });
  const impact = preview.data;
  const ready = preview.isSuccess && !preview.isFetching;

  async function start() {
    setBusy(true);
    setError(null);
    try {
      await startRetentionAction("run");
      setAsked(false);
    } catch (failure) {
      setError(refusal(failure));
    } finally {
      setBusy(false);
      // Running or not, the card reads the state again: a 409 means a pass
      // is already under way, and the card should say so.
      void client.invalidateQueries({ queryKey: retentionKey });
    }
  }

  if (data.running) return null;
  return <>
    {asked && <p className="panel-note" role="status">
      {preview.isError ? "Could not count what a pass would remove. It can still be started."
        : !ready || !impact ? "Counting what a pass would remove now…"
        : `A pass now would fold ${plural(impact.heartbeats, "raw heartbeat", "raw heartbeats")} into hourly summaries and delete ${plural(impact.hourly_buckets, "hourly summary", "hourly summaries")} and ${plural(impact.incidents, "resolved incident", "resolved incidents")}.`}
      {data.max_database_size.bytes > 0 && ` The ${formatBytes(data.max_database_size.bytes)} size limit may remove more; that depends on what the windows free, so it cannot be counted in advance.`}
    </p>}
    {error && <p className="warn-note" role="alert">{error}</p>}
    <div className="control-row">
      {asked
        ? <>
          <button className="button" type="button" disabled={busy || !(ready || preview.isError)} onClick={() => void start()}>
            {busy ? "Starting…" : "Start the pass"}
          </button>
          <button className="button" type="button" disabled={busy} onClick={() => { setAsked(false); setError(null); }}>Cancel</button>
        </>
        : <button className="button" type="button" onClick={() => { setAsked(true); setError(null); }}>Run now…</button>}
    </div>
  </>;
}

function describeSeconds(seconds: number): string {
  return seconds < 90 ? plural(seconds, "second", "seconds") : plural(Math.round(seconds / 60), "minute", "minutes");
}

/**
 * "Compact database": a full VACUUM, offered only when it would help.
 *
 * The cost is said before the button rather than behind a second press:
 * writes wait for the rewrite, for roughly the estimate. A disk that cannot
 * hold the copy disables the button and says by how much, which is the same
 * refusal the server would give (507), without making the reader ask for it.
 */
function Compact({ plan, canAdmin }: { plan: CompactPlan; canAdmin: boolean }) {
  const client = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const last = plan.last;

  async function start() {
    setBusy(true);
    setError(null);
    try {
      await startRetentionAction("compact");
    } catch (failure) {
      setError(refusal(failure));
    } finally {
      setBusy(false);
      void client.invalidateQueries({ queryKey: retentionKey });
    }
  }

  const why = plan.auto_vacuum === "incremental"
    ? `${formatBytes(plan.free_bytes)} of the ${formatBytes(plan.size_bytes)} file is empty space.`
    : `The ${formatBytes(plan.size_bytes)} file never shrinks by itself (${formatBytes(plan.free_bytes)} of it is empty now): it predates the mode that hands deleted space back to the disk.`;
  return <Panel label="Database file" spacing="form">
    {last && (last.error !== null
      ? <p className="warn-note">The last compaction failed: {last.error}</p>
      : <p>Compacted <When value={last.finished_at} /> in {formatDuration(last.duration_ms)}: {formatBytes(last.before_bytes)} to {formatBytes(last.after_bytes)}.
        {last.shrink_pending && " The file reaches that size at the next checkpoint."}</p>)}
    {plan.running ? <p role="status">Compacting the database. Checks keep running; their results are recorded when it finishes.</p>
      : plan.recommended && <>
        <p>{why} Compacting rewrites it at its contents' size and keeps it that way.</p>
        {canAdmin && <>
          <p className="panel-note">
            While it runs, which takes about {describeSeconds(plan.estimate_seconds)}, results wait and are recorded when it finishes.
            {plan.disk_shortfall && ` It needs ${formatBytes(plan.disk_shortfall.need_bytes)} free for the copy and the disk has ${formatBytes(plan.disk_shortfall.free_bytes)}, so it cannot run.`}
          </p>
          {error && <p className="warn-note" role="alert">{error}</p>}
          <div>
            <button className="button" type="button" disabled={busy || plan.disk_shortfall !== null} onClick={() => void start()}>
              {busy ? "Starting…" : "Compact database"}
            </button>
          </div>
        </>}
      </>}
  </Panel>;
}

/** The daily pass as it last ran, and what an administrator can start by hand. */
export function Maintenance({ data, canAdmin }: { data: Retention; canAdmin: boolean }) {
  const plan = data.compact;
  return <>
    <Panel label="Daily pass" spacing="form">
      {data.running && <p role="status">A pass is running. Its outcome appears here when it finishes.</p>}
      {data.last_pass ? <LastPass pass={data.last_pass} />
        : !data.running && <p>No pass recorded yet. The next runs at {data.run_at.value}{canAdmin ? ", or when started here" : ""}.</p>}
      {canAdmin && <RunNow data={data} />}
    </Panel>
    {/* Only when there is something to say: a file that compacting would
        help, one being compacted, or the outcome of the last compaction. */}
    {plan && (plan.recommended || plan.running || plan.last) && <Compact plan={plan} canAdmin={canAdmin} />}
  </>;
}
