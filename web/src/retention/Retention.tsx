import { useId, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, Panel } from "../components/Card";
import { ApiError } from "../api/http";
import {
  fetchRetention, previewRetention, retentionKey, saveRetention,
  type Retention, type RetentionTable, type RetentionWindow,
} from "./api";
import { formatBytes } from "./format";

const DAY = 86_400;

const TABLE_NAMES: Record<RetentionTable["name"], string> = {
  heartbeats: "Raw heartbeats",
  heartbeat_responses: "Failure responses",
  heartbeat_hourly: "Hourly summaries",
  incidents: "Resolved incidents",
};

const count = new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 });

/** Bytes a row of this table costs today, or null before there is anything to measure. */
function bytesPerRow(table: RetentionTable | undefined): number | null {
  return table && table.bytes !== null && table.rows > 0 ? table.bytes / table.rows : null;
}

function describeWindow(seconds: number): string {
  if (seconds === 0) return "forever";
  const days = seconds / DAY;
  return Number.isInteger(days) ? `${days} ${days === 1 ? "day" : "days"}` : `${Math.round(seconds / 3600)} hours`;
}

/**
 * What a window will cost once it is full, from what the instance measures
 * today: rows arriving per day, times the window, times what a row costs now.
 * A window kept forever has no steady state, so it is stated as a rate.
 */
function estimate(windowDays: number | null, tables: RetentionTable[], names: RetentionTable["name"][]): string | null {
  let perDay = 0;
  for (const name of names) {
    const table = tables.find((item) => item.name === name);
    const size = bytesPerRow(table);
    if (!table || size === null) return null;
    perDay += table.rows_per_day * size;
  }
  if (perDay === 0) return null;
  return windowDays === null
    ? `Grows about ${formatBytes(perDay * 365)} a year at today's rate.`
    : `About ${formatBytes(perDay * windowDays)} once full, at today's rate.`;
}

type Draft = { days: string; forever: boolean };

function draftOf(window: RetentionWindow): Draft {
  return window.seconds === 0 ? { days: "", forever: true } : { days: String(window.seconds / DAY), forever: false };
}

function secondsOf(draft: Draft): number | null {
  if (draft.forever) return 0;
  const days = Number(draft.days);
  return draft.days.trim() !== "" && Number.isFinite(days) && days > 0 ? Math.round(days * DAY) : null;
}

function WindowField({ label, window, draft, onChange, disabled, estimateText, error }: {
  label: string; window: RetentionWindow; draft: Draft; onChange: (draft: Draft) => void;
  disabled: boolean; estimateText: string | null; error?: string;
}) {
  const id = useId();
  const pinned = window.source === "pinned";
  const help = [`${id}-help`, error ? `${id}-error` : ""].filter(Boolean).join(" ");
  return (
    <fieldset className="retention-field" disabled={disabled || pinned}>
      <legend className="auth-label">{label}</legend>
      <div className="retention-inputs">
        <input
          className="auth-input retention-days" id={`${id}-days`} type="number" min="1" step="1" inputMode="numeric"
          aria-label={`${label}, in days`} aria-describedby={help} aria-invalid={error ? true : undefined}
          value={draft.forever ? "" : draft.days} disabled={draft.forever}
          onChange={(event) => onChange({ ...draft, days: event.target.value })}
        />
        <span aria-hidden="true">days</span>
        <label><input type="checkbox" checked={draft.forever}
          onChange={(event) => onChange({ days: draft.days || String(window.seconds / DAY || 30), forever: event.target.checked })} /> Forever</label>
      </div>
      <p className="auth-note" id={`${id}-help`}>
        {pinned
          ? `Set by ${window.pinned_by} to ${describeWindow(window.seconds)}; change it there.`
          : estimateText ?? "No growth measured yet."}
        {window.set_aside_seconds !== null && ` The saved ${describeWindow(window.set_aside_seconds)} is set aside because it conflicts with the pinned window.`}
      </p>
      {error && <p className="auth-error" id={`${id}-error`} role="alert">{error}</p>}
    </fieldset>
  );
}

function RetentionForm({ data, canAdmin, saved, setSaved }: {
  data: Retention; canAdmin: boolean; saved: boolean; setSaved: (saved: boolean) => void;
}) {
  const client = useQueryClient();
  const [raw, setRaw] = useState(() => draftOf(data.raw));
  const [rollup, setRollup] = useState(() => draftOf(data.rollup));
  const [saving, setSaving] = useState(false);
  const [rejection, setRejection] = useState<{ field?: string; message: string } | null>(null);

  const rawSeconds = secondsOf(raw);
  const rollupSeconds = secondsOf(rollup);
  const changed = rawSeconds !== data.raw.seconds || rollupSeconds !== data.rollup.seconds;
  const valid = rawSeconds !== null && rollupSeconds !== null;
  const shorter = valid && (
    (rawSeconds !== 0 && (data.raw.seconds === 0 || rawSeconds < data.raw.seconds)) ||
    (rollupSeconds !== 0 && (data.rollup.seconds === 0 || rollupSeconds < data.rollup.seconds)));
  // Asked only for a shorter window, which is the one change that removes
  // anything, so the page says what it costs before it is saved.
  const preview = useQuery({
    queryKey: ["retention-preview", rawSeconds, rollupSeconds],
    queryFn: ({ signal }) => previewRetention(rawSeconds ?? 0, rollupSeconds ?? 0, signal),
    enabled: canAdmin && changed && shorter,
    retry: false,
  });

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!valid || saving) return;
    const body: { raw_seconds?: number; rollup_seconds?: number } = {};
    if (data.raw.source !== "pinned") body.raw_seconds = rawSeconds;
    if (data.rollup.source !== "pinned") body.rollup_seconds = rollupSeconds;
    setSaving(true);
    setRejection(null);
    setSaved(false);
    try {
      client.setQueryData(retentionKey, await saveRetention(body));
      setSaved(true);
    } catch (error) {
      setRejection(error instanceof ApiError
        ? { message: error.message, field: error.field ?? undefined }
        : { message: "Could not reach SubGlance. Check your connection and try again." });
    } finally {
      setSaving(false);
    }
  }

  const impact = preview.data;
  const removes = impact && (impact.heartbeats > 0 || impact.hourly_buckets > 0 || impact.incidents > 0);
  return (
    <form className="auth-form" aria-label="Retention" onSubmit={submit}>
      <WindowField label="Keep raw heartbeats" window={data.raw} draft={raw} disabled={!canAdmin || saving}
        onChange={(next) => { setRaw(next); setSaved(false); }}
        estimateText={estimate(raw.forever ? null : Number(raw.days) || null, data.tables, ["heartbeats", "heartbeat_responses"])}
        error={rejection?.field === "raw_seconds" ? rejection.message : undefined} />
      <WindowField label="Keep hourly summaries and resolved incidents" window={data.rollup} draft={rollup} disabled={!canAdmin || saving}
        onChange={(next) => { setRollup(next); setSaved(false); }}
        estimateText={estimate(rollup.forever ? null : Number(rollup.days) || null, data.tables, ["heartbeat_hourly", "incidents"])}
        error={rejection?.field === "rollup_seconds" ? rejection.message : undefined} />
      {!canAdmin && <p className="auth-note">Only an administrator can change retention.</p>}
      {canAdmin && changed && shorter && (
        <p className="auth-note" role="status">
          {preview.isError ? "Could not count what this change removes."
            : !impact ? "Counting what this change removes…"
            : removes ? `The next daily pass will fold ${count.format(impact.heartbeats)} raw heartbeats into hourly summaries and delete ${count.format(impact.hourly_buckets)} hourly summaries and ${count.format(impact.incidents)} resolved incidents.`
            : "Nothing is old enough to be removed by this change yet."}
        </p>
      )}
      {rejection && !rejection.field && <p className="auth-error" role="alert">{rejection.message}</p>}
      {canAdmin && (
        <div>
          <button className="auth-submit" type="submit" disabled={!changed || !valid || saving}>
            {saving ? "Saving…" : "Save retention"}
          </button>
        </div>
      )}
      {saved && <p role="status">Saved. The new windows apply from the next daily pass.</p>}
    </form>
  );
}

/** Retention windows, what each table costs today, and what a change would remove. */
export function RetentionCard({ canAdmin }: { canAdmin: boolean }) {
  const query = useQuery({ queryKey: retentionKey, queryFn: ({ signal }) => fetchRetention(signal) });
  // Held here rather than in the form: a save remounts the form (see its
  // key), and the confirmation has to outlive that.
  const [saved, setSaved] = useState(false);
  const data = query.data;
  return (
    <Card title="Retention & storage" className="retention-card">
      <Panel>
        {!data ? <p>{query.isError ? "Retention settings unavailable." : "Loading retention settings…"}</p> : <>
          <table className="retention-tables">
            <caption className="auth-label">Database today</caption>
            <thead><tr><th scope="col">Table</th><th scope="col">Rows</th><th scope="col">Size</th><th scope="col">Added per day</th></tr></thead>
            <tbody>
              {data.tables.map((table) => (
                <tr key={table.name}>
                  <th scope="row">{TABLE_NAMES[table.name]}</th>
                  <td>{count.format(table.rows)}</td>
                  <td>{table.bytes === null ? "—" : formatBytes(table.bytes)}</td>
                  <td>{count.format(table.rows_per_day)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {/* Keyed on the windows in force, so a save or a refetch that changes
              them starts the form again from what the server now says. */}
          <RetentionForm key={`${data.raw.seconds}/${data.rollup.seconds}`} data={data} canAdmin={canAdmin} saved={saved} setSaved={setSaved} />
        </>}
      </Panel>
    </Card>
  );
}
