import { useId, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, Panel } from "../components/Card";
import { Checkbox } from "../components/Choice";
import { IconDatabase } from "../components/icons";
import { ApiError } from "../api/http";
import {
  fetchRetention, previewRetention, retentionKey, saveRetention,
  type Retention, type RetentionChange, type RetentionTable, type RetentionWindow,
} from "./api";
import { Maintenance } from "./Maintenance";
import { formatBytes } from "./format";
import { formatCount } from "../format/format";

const DAY = 86_400;

const TABLE_NAMES: Record<RetentionTable["name"], string> = {
  heartbeats: "Raw heartbeats",
  heartbeat_responses: "Failure responses",
  heartbeat_hourly: "Hourly summaries",
  incidents: "Resolved incidents",
};


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

/** An amount in a number box, or its switched-off state ("Forever", "No limit"). */
type Draft = { value: string; off: boolean };

function draftOf(window: RetentionWindow): Draft {
  return window.seconds === 0 ? { value: "", off: true } : { value: String(window.seconds / DAY), off: false };
}

function secondsOf(draft: Draft): number | null {
  if (draft.off) return 0;
  const days = Number(draft.value);
  return draft.value.trim() !== "" && Number.isFinite(days) && days > 0 ? Math.round(days * DAY) : null;
}

/*
 * The size limit is typed in decimal megabytes, the unit the rest of the card
 * reports sizes in (`formatBytes`), so the number typed is the number read
 * beside it. The server's minimum is 32 MiB, which is 33.6 MB: the field asks
 * for the next whole megabyte rather than a fraction nobody would type.
 */
const MB = 1_000_000;

function sizeDraftOf(bytes: number): Draft {
  return bytes === 0 ? { value: "", off: true } : { value: String(Math.round(bytes / MB)), off: false };
}

function bytesOf(draft: Draft, minimum: number): number | null {
  if (draft.off) return 0;
  const mb = Number(draft.value);
  const bytes = Math.round(mb * MB);
  return draft.value.trim() !== "" && Number.isFinite(mb) && bytes >= minimum ? bytes : null;
}

/** A number box with a unit and a switch that turns the amount off, as one fieldset. */
function AmountField({ label, unit, offLabel, restore, draft, onChange, disabled, locked, note, error, min }: {
  label: string; unit: string; offLabel: string; restore: string; draft: Draft; onChange: (draft: Draft) => void;
  disabled: boolean; locked: boolean; note: string; error?: string; min: number;
}) {
  const id = useId();
  const help = [`${id}-help`, error ? `${id}-error` : ""].filter(Boolean).join(" ");
  return (
    <fieldset className="retention-field" disabled={disabled || locked}>
      <legend className="field-label">{label}</legend>
      <div className="control-row">
        <input
          className="input input--inset retention-days" type="number" min={min} step="1" inputMode="numeric"
          aria-label={`${label}, in ${unit}`} aria-describedby={help} aria-invalid={error ? true : undefined}
          value={draft.off ? "" : draft.value} disabled={draft.off}
          // A dash, not an empty box: switched off, the amount does not apply,
          // and an empty disabled field read as one that had failed to load.
          placeholder={draft.off ? "—" : undefined}
          onChange={(event) => onChange({ ...draft, value: event.target.value })}
        />
        <span aria-hidden="true">{unit}</span>
        <Checkbox checked={draft.off}
          onChange={(event) => onChange({ value: draft.value || restore, off: event.target.checked })}>{offLabel}</Checkbox>
      </div>
      <p className="panel-note" id={`${id}-help`}>{note}</p>
      {error && <p className="field-error" id={`${id}-error`} role="alert">{error}</p>}
    </fieldset>
  );
}

function windowNote(window: RetentionWindow, estimateText: string | null): string {
  const base = window.source === "pinned"
    ? `Set by ${window.pinned_by} to ${describeWindow(window.seconds)}; change it there.`
    : estimateText ?? "No growth measured yet.";
  return window.set_aside_seconds === null ? base
    : `${base} The saved ${describeWindow(window.set_aside_seconds)} is set aside because it conflicts with the pinned window.`;
}

/**
 * What a new or lower size limit does, from what the data occupies today.
 * Unlike a window it cannot be counted in advance: how much it removes
 * depends on how much the windows free first.
 */
function limitNote(limit: number, data: Retention): string {
  const plan = data.compact;
  const used = plan ? plan.size_bytes - plan.free_bytes : null;
  const order = "raw detail first, then the oldest hourly summaries, never incidents or the last day";
  if (used !== null && used <= limit) return `The data takes ${formatBytes(used)} today, so this removes nothing yet. Past it, the daily pass removes ${order}.`;
  return `${used === null ? "Over this limit" : `The data takes ${formatBytes(used)} today, so`} the next daily pass removes history beyond the windows until it is under: ${order}. How much cannot be counted in advance.`;
}

/**
 * What the size field offers when "No limit" is switched off: twice what the
 * data takes today, rounded up to 100 MB, so switching the limit on removes
 * nothing until the database has doubled. A number that deleted history the
 * moment it was saved would be a trap in a default.
 */
function suggestedLimitMB(data: Retention, minimumMB: number): number {
  if (data.max_database_size.bytes > 0) return Math.round(data.max_database_size.bytes / MB);
  const used = data.compact ? data.compact.size_bytes - data.compact.free_bytes : 0;
  return Math.max(minimumMB, Math.ceil((2 * used) / (100 * MB)) * 100);
}

type Outcome = "saved" | "stale" | null;

/** The server's time-of-day setting, read-only when a flag pinned it. */
function RunAtField({ data, value, onChange, disabled, error }: {
  data: Retention; value: string; onChange: (value: string) => void; disabled: boolean; error?: string;
}) {
  const id = useId();
  const pin = data.run_at;
  return (
    <div className="field">
      <label className="field-label" htmlFor={id}>Run the daily pass at</label>
      <input className="input input--inset retention-time" id={id} type="time" step="60" required value={value}
        disabled={disabled || pin.source === "pinned"} aria-invalid={error ? true : undefined}
        aria-describedby={`${id}-help${error ? ` ${id}-error` : ""}`} onChange={(event) => onChange(event.target.value)} />
      <p className="panel-note" id={`${id}-help`}>
        {pin.source === "pinned" ? `Set by ${pin.pinned_by} to ${pin.value}; change it there.`
          : "In the server's time zone (its TZ variable, UTC without one); the times on this card are in yours. A new time never starts a pass by itself."}
      </p>
      {error && <p className="field-error" id={`${id}-error`} role="alert">{error}</p>}
    </div>
  );
}

function RetentionForm({ data, canAdmin, outcome, setOutcome }: {
  data: Retention; canAdmin: boolean; outcome: Outcome; setOutcome: (outcome: Outcome) => void;
}) {
  const client = useQueryClient();
  const [raw, setRaw] = useState(() => draftOf(data.raw));
  const [rollup, setRollup] = useState(() => draftOf(data.rollup));
  const [limit, setLimit] = useState(() => sizeDraftOf(data.max_database_size.bytes));
  const [runAt, setRunAt] = useState(data.run_at.value);
  const [saving, setSaving] = useState(false);
  const [rejection, setRejection] = useState<{ field?: string; message: string } | null>(null);

  const rawSeconds = secondsOf(raw);
  const rollupSeconds = secondsOf(rollup);
  const size = data.max_database_size;
  // The box holds whole megabytes, so a limit set in MiB by a flag or the API
  // reads back rounded. Untouched, it stands for the exact value in force,
  // or the form would count the rounding as a change and save it.
  const limitBytes = limit.off === (size.bytes === 0) && limit.value === sizeDraftOf(size.bytes).value
    ? size.bytes : bytesOf(limit, size.minimum_bytes);
  const runAtValid = /^([01]\d|2[0-3]):[0-5]\d$/.test(runAt);
  const windowsChanged = rawSeconds !== data.raw.seconds || rollupSeconds !== data.rollup.seconds;
  const changed = windowsChanged || limitBytes !== size.bytes || runAt !== data.run_at.value;
  const valid = rawSeconds !== null && rollupSeconds !== null && limitBytes !== null && runAtValid;
  const shorter = rawSeconds !== null && rollupSeconds !== null && (
    (rawSeconds !== 0 && (data.raw.seconds === 0 || rawSeconds < data.raw.seconds)) ||
    (rollupSeconds !== 0 && (data.rollup.seconds === 0 || rollupSeconds < data.rollup.seconds)));
  // A new or lower limit is the size-limit counterpart of a shorter window:
  // it can remove history on the next pass, so the page says what it does.
  const tighter = limitBytes !== null && limitBytes !== 0 && (size.bytes === 0 || limitBytes < size.bytes);
  // Asked only for a shorter window, which is the one change that removes
  // anything, so the page says what it costs before it is saved.
  const preview = useQuery({
    queryKey: ["retention-preview", rawSeconds, rollupSeconds],
    queryFn: ({ signal }) => previewRetention(rawSeconds ?? 0, rollupSeconds ?? 0, signal),
    enabled: canAdmin && windowsChanged && shorter,
    retry: false,
  });
  // A shorter window can delete rows on the next pass, so it is not saved
  // until the page has been able to say how many. A cached count for a draft
  // previewed earlier is being read again, so Save waits for that answer too.
  const previewReady = !shorter || (preview.isSuccess && !preview.isFetching);
  const edit = <T,>(set: (value: T) => void) => (value: T) => { set(value); setOutcome(null); };

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!valid || saving || !previewReady) return;
    // Without a version the save could only be unconditional, which is the
    // overwrite the version exists to prevent. Refuse rather than downgrade.
    if (!data.etag) {
      setRejection({ message: "Reload the page before saving: the server did not say which version of the settings this is." });
      return;
    }
    // Only what is not pinned: a pinned field would be refused with a 409.
    const body: RetentionChange = {};
    if (data.raw.source !== "pinned") body.raw_seconds = rawSeconds;
    if (data.rollup.source !== "pinned") body.rollup_seconds = rollupSeconds;
    if (data.run_at.source !== "pinned") body.run_at = runAt;
    if (size.source !== "pinned") body.max_database_bytes = limitBytes;
    setSaving(true);
    setRejection(null);
    setOutcome(null);
    try {
      const next = await saveRetention(body, data.etag);
      if (next) client.setQueryData(retentionKey, next);
      // The settings are saved either way; the measurements that did not come
      // back with them are read again rather than shown as an empty table.
      if (!next || next.tables.length === 0) void client.invalidateQueries({ queryKey: retentionKey });
      setOutcome("saved");
    } catch (error) {
      // Someone saved since this page read the settings. The draft was judged
      // against settings that are no longer in force — a change that looked
      // longer may now be shorter, and was never previewed — so it is not
      // retried. The settings are read again and the form restarts from them.
      if (error instanceof ApiError && error.status === 412) {
        setOutcome("stale");
        void client.invalidateQueries({ queryKey: retentionKey });
        return;
      }
      setRejection(error instanceof ApiError
        ? { message: error.message, field: error.field ?? undefined }
        : { message: "Could not reach SubGlance. Check your connection and try again." });
    } finally {
      setSaving(false);
    }
  }

  const impact = preview.data;
  const removes = impact && (impact.heartbeats > 0 || impact.hourly_buckets > 0 || impact.incidents > 0);
  const locked = !canAdmin || saving;
  const minimumMB = Math.ceil(size.minimum_bytes / MB);
  return (
    <form className="stack" aria-label="Retention" onSubmit={submit}>
      <AmountField label="Keep raw heartbeats" unit="days" offLabel="Forever" restore={String(data.raw.seconds / DAY || 30)}
        draft={raw} onChange={edit(setRaw)} disabled={locked} locked={data.raw.source === "pinned"} min={1}
        note={windowNote(data.raw, estimate(raw.off ? null : Number(raw.value) || null, data.tables, ["heartbeats", "heartbeat_responses"]))}
        error={rejection?.field === "raw_seconds" ? rejection.message : undefined} />
      <AmountField label="Keep hourly summaries and resolved incidents" unit="days" offLabel="Forever" restore={String(data.rollup.seconds / DAY || 30)}
        draft={rollup} onChange={edit(setRollup)} disabled={locked} locked={data.rollup.source === "pinned"} min={1}
        note={windowNote(data.rollup, estimate(rollup.off ? null : Number(rollup.value) || null, data.tables, ["heartbeat_hourly", "incidents"]))}
        error={rejection?.field === "rollup_seconds" ? rejection.message : undefined} />
      <AmountField label="Limit the database to" unit="MB" offLabel="No limit" restore={String(suggestedLimitMB(data, minimumMB))}
        draft={limit} onChange={edit(setLimit)} disabled={locked} locked={size.source === "pinned"} min={minimumMB}
        note={size.source === "pinned" ? `Set by ${size.pinned_by} to ${size.bytes === 0 ? "no limit" : formatBytes(size.bytes)}; change it there.`
          : limitBytes === null ? `At least ${minimumMB} MB, or no limit.`
          : tighter ? limitNote(limitBytes, data)
          : "Off by default: removing history the windows would keep is a choice. The windows above always apply."}
        error={rejection?.field === "max_database_bytes" ? rejection.message : undefined} />
      <RunAtField data={data} value={runAt} onChange={edit(setRunAt)} disabled={locked}
        error={rejection?.field === "run_at" ? rejection.message : undefined} />
      {!canAdmin && <p className="panel-note">Only an administrator can change retention.</p>}
      {canAdmin && windowsChanged && shorter && (
        <p className="panel-note" role="status">
          {preview.isError ? "Could not count what this change removes."
            : !impact || preview.isFetching ? "Counting what this change removes…"
            : removes ? `The next daily pass will fold ${formatCount(impact.heartbeats)} raw heartbeats into hourly summaries and delete ${formatCount(impact.hourly_buckets)} hourly summaries and ${formatCount(impact.incidents)} resolved incidents.`
            : "Nothing is old enough to be removed by this change yet."}
        </p>
      )}
      {rejection && !rejection.field && <p className="field-error" role="alert">{rejection.message}</p>}
      {canAdmin && (
        <div>
          <button className="button-solid" type="submit" disabled={!changed || !valid || saving || !previewReady}>
            {saving ? "Saving…" : "Save retention"}
          </button>
        </div>
      )}
      {outcome && <p role={outcome === "stale" ? "alert" : "status"}>{outcome === "stale"
        ? "Retention was changed by someone else while you were editing. The form now shows what is in force; check it and save again."
        : "Saved. The new settings apply from the next daily pass."}</p>}
    </form>
  );
}

/**
 * Retention: what each table costs today, the windows, size limit and time
 * of day the daily pass applies, what a change would remove, the last pass,
 * and the two things an administrator can start by hand.
 */
export function RetentionCard({ canAdmin }: { canAdmin: boolean }) {
  const query = useQuery({
    queryKey: retentionKey, queryFn: ({ signal }) => fetchRetention(signal),
    // A pass or a compaction runs in the background; its outcome is read
    // back from here, so the card follows it until it is done.
    refetchInterval: (current) => current.state.data?.running || current.state.data?.compact?.running ? 2_000 : false,
  });
  // Held here rather than in the form: a save, or a reload after a refused
  // one, remounts the form (see its key), and the message has to outlive it.
  const [outcome, setOutcome] = useState<Outcome>(null);
  const data = query.data;
  return (
    <Card title="Retention & storage" icon={<IconDatabase />}>
      <Panel spacing="form">
        {!data ? <p>{query.isError ? "Retention settings unavailable." : "Loading retention settings…"}</p> : <>
          <table className="retention-tables">
            <caption className="field-label">Database today</caption>
            <thead><tr><th scope="col">Table</th><th scope="col">Rows</th><th scope="col">Size</th><th scope="col">Added per day</th></tr></thead>
            <tbody>
              {data.tables.map((table) => (
                <tr key={table.name}>
                  <th scope="row">{TABLE_NAMES[table.name]}</th>
                  <td>{formatCount(table.rows)}</td>
                  <td>{table.bytes === null ? "—" : formatBytes(table.bytes)}</td>
                  <td>{formatCount(table.rows_per_day)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {/* Keyed on the settings in force, so a save or a refetch that changes
              them starts the form again from what the server now says. */}
          <RetentionForm key={[data.raw.seconds, data.rollup.seconds, data.max_database_size.bytes, data.run_at.value].join("/")}
            data={data} canAdmin={canAdmin} outcome={outcome} setOutcome={setOutcome} />
        </>}
      </Panel>
      {data && <Maintenance data={data} canAdmin={canAdmin} />}
    </Card>
  );
}
