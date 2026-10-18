import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, apiJSON, apiPost, apiRequest } from "../api/http";
import { Checkbox } from "../components/Choice";
import { DangerButton } from "../components/DangerButton";
import { windowCovers, type MaintenanceMonitor } from "./maintenanceScope";
import { DurationField } from "./DurationField";
import { MINUTES_TO_HOURS } from "./duration";
import { Select } from "../components/Select";
import { FieldError } from "../components/FieldError";
import { refusalInput } from "./maintenanceFields";
import { browserTimezone, knownTimezones } from "../notifications/quietHours";
import { formatCount } from "../format/format";
import { localInstant, oneOffSpan, STATE_WORDS, weeklySpan, windowState, type WindowState } from "./maintenanceTimes";

type Window = {
  id: number; name: string; monitor_id?: number; tag_key?: string; tag_value?: string;
  starts_at?: string; ends_at?: string; timezone?: string; weekdays?: number[];
  local_time?: string; duration_minutes?: number; active: boolean;
};
const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
const KEY = ["maintenance"];
/** Ended windows go to the bottom of the list, the rest keep the order they were made in. */
const RANK: Record<WindowState, number> = { active: 0, upcoming: 0, ended: 1 };
const windowsWord = (n: number) => `${formatCount(n)} ended ${n === 1 ? "window" : "windows"}`;

/**
 * The maintenance schedule and the form that adds to it.
 *
 * `focus` narrows it to one monitor, for the detail page: the list shows only
 * the windows that cover that monitor, and the form starts on it. Every other
 * monitor stays choosable, because "this one and its database" is a normal
 * thing to be planning when the form is open.
 *
 * A default export, loaded with `lazy()`: nobody needs the schedule until a
 * drawer opens on it, so it does not ride in the bundle every page pays for.
 */
export default function MaintenanceManager({ monitors, canWrite, focus }: { monitors: readonly MaintenanceMonitor[]; canWrite: boolean; focus?: MaintenanceMonitor }) {
  const queryClient = useQueryClient();
  const id = useId();
  const [scope, setScope] = useState("monitor");
  const [weekly, setWeekly] = useState(false);
  // In minutes, the API's unit; the field shows it in minutes or hours.
  const [minutes, setMinutes] = useState("60");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  // The duration's own error sits under that field (DESIGN.md §7.2), not in
  // the form-wide alert, which is for what the server says.
  const [durationError, setDurationError] = useState("");
  // A server refusal that names an input the form draws stands under that
  // input too; one that names none stays in the alert. It goes when anything
  // in the form changes, since the change may be the fix.
  const [refusal, setRefusal] = useState<{ field: string; message: string } | null>(null);
  const form = useRef<HTMLFormElement>(null);
  // Set when a refusal arrives, so the input it names takes focus once, not
  // again every time a later cancel locks and unlocks the form.
  const focusRefusal = useRef(false);
  useEffect(() => {
    // The refusal arrives while the fieldset is disabled; focus once it unlocks.
    if (busy || refusal === null || !focusRefusal.current) return;
    focusRefusal.current = false;
    const control = refusal.field === "duration_minutes"
      ? document.getElementById(`${id}-duration`)
      : form.current?.querySelector<HTMLElement>(`[name="${refusal.field}"]`);
    control?.focus();
  }, [busy, refusal, id]);
  const windows = useQuery({ queryKey: KEY, queryFn: async ({ signal }) => {
    const data = await apiJSON<{ maintenance: Window[] }>("/api/v1/maintenance", { signal });
    if (!Array.isArray(data.maintenance)) throw new Error("The server did not return maintenance windows.");
    return data.maintenance;
  }, refetchInterval: 15_000 });
  // Where each window stands is judged at the moment the list was read, the
  // same moment the server judged `active` at, so the two cannot disagree.
  const readAt = windows.dataUpdatedAt;
  const covered = focus === undefined ? windows.data : windows.data?.filter((window) => windowCovers(window, focus));
  const shown = covered?.map((window) => ({ window, state: windowState(window, readAt) }))
    .sort((a, b) => RANK[a.state] - RANK[b.state]);
  // Only one-off windows end; a weekly one always has another occurrence.
  const ended = shown?.filter((entry) => entry.state === "ended").map((entry) => entry.window) ?? [];
  // The question before removing ended windows, and the counts it was asked
  // about: the list can poll while it is open, and the answer is to the
  // question on screen.
  const [clearing, setClearing] = useState<readonly Window[] | null>(null);
  const keepButton = useRef<HTMLButtonElement>(null);
  const clearButton = useRef<HTMLButtonElement>(null);
  const returnFocus = useRef(false);
  // Where focus goes when the button that asked is gone, because every
  // ended window was removed: the line that says what happened.
  const statusLine = useRef<HTMLParagraphElement>(null);
  useEffect(() => {
    if (clearing !== null) keepButton.current?.focus();
    else if (returnFocus.current) { returnFocus.current = false; (clearButton.current ?? statusLine.current)?.focus(); }
  }, [clearing]);
  const zone = browserTimezone();
  const zones = weekly ? knownTimezones() : [];
  const refresh = async () => { await Promise.all([
    queryClient.invalidateQueries({ queryKey: KEY }),
    queryClient.invalidateQueries({ queryKey: ["monitors"] }),
    queryClient.invalidateQueries({ queryKey: ["monitor-detail"] }),
    queryClient.invalidateQueries({ queryKey: ["incidents", "open"] }),
  ]); };
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) return;
    const form = event.currentTarget;
    const data = new FormData(form);
    const value = (key: string) => String(data.get(key) ?? "");
    // The number box no longer carries the browser's own min and max, since
    // it may hold hours; the range is checked here, in the units it is shown in.
    const length = Number(minutes);
    if (weekly && !(Number.isInteger(length) && length >= 1 && length <= 1440)) {
      setMessage(""); setError(""); setRefusal(null); setDurationError("The duration must be between 1 min and 24 h, in whole minutes."); return;
    }
    setDurationError("");
    // A one-off window is typed on the reader's clock and sent as the instant
    // it names. A time the clocks skip has no instant, and is refused here,
    // under its field, rather than moved an hour without a word.
    const refuse = (field: string, text: string) => { setMessage(""); setError(""); focusRefusal.current = true; setRefusal({ field, message: text }); };
    let span: { starts_at: string; ends_at: string } | null = null;
    if (!weekly) {
      const start = localInstant(value("starts_at"));
      if (!start.ok) { refuse("starts_at", start.message); return; }
      const end = localInstant(value("ends_at"));
      if (!end.ok) { refuse("ends_at", end.message); return; }
      span = { starts_at: start.iso, ends_at: end.iso };
    }
    const body = { name: value("name"),
      ...(scope === "monitor" ? { monitor_id: Number(value("monitor_id")) } : { tag_key: value("tag_key"), tag_value: value("tag_value") }),
      ...(span === null ? { timezone: value("timezone"), local_time: value("local_time"), weekdays: data.getAll("weekdays").map(Number), duration_minutes: Number(value("duration_minutes")) }
        : span),
    };
    setBusy(true); setError(""); setRefusal(null); setMessage("");
    try { await apiPost("/api/v1/maintenance", body); await refresh(); setMessage("Maintenance scheduled."); }
    catch (e) {
      // The scope and schedule cannot change while the request is out, so
      // they are the form the refusal is about.
      const field = e instanceof ApiError ? refusalInput(e.field, scope, weekly) : null;
      const text = e instanceof Error ? e.message : "Could not schedule maintenance.";
      if (field === null) setError(text);
      else { focusRefusal.current = true; setRefusal({ field, message: text }); }
    }
    finally { setBusy(false); }
  }
  async function cancel(window: Window, state: WindowState) {
    if (busy) return;
    setBusy(true); setError(""); setMessage("");
    try { await apiRequest(`/api/v1/maintenance/${window.id}`, { method: "DELETE" }); await refresh(); setMessage(`${state === "ended" ? "Removed" : "Cancelled"} ${window.name}. Recorded history is unchanged.`); }
    catch (e) { setError(e instanceof Error ? e.message : "Could not cancel maintenance."); }
    finally { setBusy(false); }
  }
  /**
   * Removes the ended one-off windows the question named, one request each,
   * since the API cancels one window at a time. It stops at the first
   * failure and says how far it got: the rest are still listed, still ended,
   * and the button is there to try again.
   */
  async function removeEnded(targets: readonly Window[]) {
    if (busy) return;
    setBusy(true); setError(""); setMessage("");
    let removed = 0;
    try {
      for (const window of targets) {
        await apiRequest(`/api/v1/maintenance/${window.id}`, { method: "DELETE" });
        removed++;
      }
      setMessage(`Removed ${windowsWord(removed)}. Recorded history is unchanged.`);
    } catch (e) {
      const reason = e instanceof Error ? e.message : "the server did not answer";
      setError(`Removed ${formatCount(removed)} of ${windowsWord(targets.length)}, then stopped: ${reason}`);
    } finally {
      await refresh().catch(() => undefined);
      // Together, so the list is unlocked when focus goes back to the
      // button; it is there again only if some ended windows are left.
      returnFocus.current = true;
      setBusy(false); setClearing(null);
    }
  }
  const keep = () => { returnFocus.current = true; setClearing(null); };
  const blamed = (field: string) => refusal?.field === field ? refusal.message : undefined;
  const errorId = (field: string) => `${id}-${field}-error`;
  // What an input wears when a refusal names it: the red border, and the
  // refusal read before any help the input already points at.
  const invalid = (field: string, help?: string) => {
    const refused = blamed(field) !== undefined;
    const describedBy = [refused ? errorId(field) : "", help ?? ""].filter(Boolean).join(" ");
    return { "aria-invalid": refused ? true as const : undefined, "aria-describedby": describedBy || undefined };
  };
  const refusalFor = (field: string) => {
    const text = blamed(field);
    return text === undefined ? null : <FieldError id={errorId(field)}>{text}</FieldError>;
  };
  const durationMessage = durationError || blamed("duration_minutes");
  return <div className="maintenance-content">
    <p>Checks continue. Alerts are suppressed and maintenance checks are excluded from uptime. Paused monitors stay paused. Overlapping windows remain active until all have ended.</p>
    {windows.isPending ? <p role="status">Loading maintenance…</p> : null}
    {/* A failed first read has nothing to be out of date; only a failed poll does. */}
    {windows.error ? <p role="alert">{windows.data === undefined
      ? `Could not load maintenance: ${windows.error.message}`
      : `Could not refresh maintenance: ${windows.error.message}. Showing the last loaded schedules, which may be out of date.`}</p> : null}
    {shown?.length === 0 ? <p>{focus ? `No maintenance windows cover ${focus.name}.` : "No maintenance windows scheduled."}</p> : null}
    <ul className="maintenance-list">{shown?.map(({ window, state }) => <li key={window.id} data-state={state}>
      <strong>{window.name}</strong> — {STATE_WORDS[state]}
      <p>{window.monitor_id ? monitors.find((m) => m.id === String(window.monitor_id))?.name ?? `Monitor ${window.monitor_id}` : `${window.tag_key}:${window.tag_value}`}</p>
      <p>{window.timezone ? weeklySpan(window.weekdays, window.local_time, window.timezone, window.duration_minutes)
        : oneOffSpan(window.starts_at ?? "", window.ends_at ?? "")}</p>
      {canWrite ? <button className="button" type="button" disabled={busy} aria-label={`${state === "ended" ? "Remove" : "Cancel"} maintenance ${window.name}`} onClick={() => void cancel(window, state)}>{state === "ended" ? "Remove" : "Cancel maintenance"}</button> : null}
    </li>)}</ul>
    {canWrite && clearing === null && ended.length > 0 ? <div className="button-row maintenance-clear">
      <button ref={clearButton} className="button" type="button" disabled={busy} onClick={() => setClearing(ended)}>Remove ended windows</button>
    </div> : null}
    {canWrite && clearing !== null ? <div role="group" className="maintenance-clear" aria-labelledby={`${id}-clear-question`}>
      <p id={`${id}-clear-question`}>Remove {windowsWord(clearing.length)}? They no longer apply. Checks they excluded from uptime stay excluded.</p>
      <div className="button-row">
        <DangerButton disabled={busy} onClick={() => void removeEnded(clearing)}>{busy ? "Removing…" : `Remove ${windowsWord(clearing.length)}`}</DangerButton>
        <button ref={keepButton} className="button" type="button" disabled={busy} onClick={keep}>Keep them</button>
      </div>
    </div> : null}
    {canWrite ? <form ref={form} className="form-column" onSubmit={(event) => void submit(event)} onChange={() => setRefusal(null)} aria-label="Schedule maintenance">
      <fieldset disabled={busy} className="maintenance-fields">
        <div className="field"><label htmlFor={`${id}-name`}>Name</label><input id={`${id}-name`} className="input" name="name" required maxLength={120} {...invalid("name")}/>{refusalFor("name")}</div>
        <label className="field">Applies to<Select className="input" name="scope" value={scope} onChange={(e) => setScope(e.target.value)}><option value="monitor">One monitor</option><option value="tag">Tag group</option></Select></label>
        {scope === "monitor" ? <div className="field"><label htmlFor={`${id}-monitor`}>Monitor</label><Select id={`${id}-monitor`} className="input" name="monitor_id" required defaultValue={focus?.id ?? ""} {...invalid("monitor_id")}><option value="">Choose a monitor</option>{monitors.map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}</Select>{refusalFor("monitor_id")}</div>
          : <div className="field-grid">
            <div className="field"><label htmlFor={`${id}-tag-key`}>Tag key</label><input id={`${id}-tag-key`} className="input" name="tag_key" required placeholder="env" {...invalid("tag_key")}/>{refusalFor("tag_key")}</div>
            <div className="field"><label htmlFor={`${id}-tag-value`}>Tag value</label><input id={`${id}-tag-value`} className="input" name="tag_value" required placeholder="prod" {...invalid("tag_value")}/>{refusalFor("tag_value")}</div>
          </div>}
        <label className="field">Schedule<Select className="input" name="schedule" value={weekly ? "weekly" : "once"} onChange={(e) => setWeekly(e.target.value === "weekly")}><option value="once">One-off</option><option value="weekly">Weekly</option></Select></label>
        {weekly ? <>
          {/* The browser's zone, as quiet hours start from: a window planned
              for "Sunday night" is meant on the planner's clock. */}
          <div className="field"><label htmlFor={`${id}-timezone`}>Timezone</label><input id={`${id}-timezone`} className="input" name="timezone" required defaultValue={zone} placeholder="Europe/Amsterdam" autoComplete="off" spellCheck={false} list={zones.length > 0 ? `${id}-zones` : undefined} {...invalid("timezone", `${id}-dst`)}/>{refusalFor("timezone")}
            {zones.length > 0 ? <datalist id={`${id}-zones`}>{zones.map((name) => <option key={name} value={name}>{name}</option>)}</datalist> : null}</div>
          {/* The group is what a weekdays refusal is about, not any one box in it. */}
          <fieldset className="maintenance-days" {...invalid("weekdays")}><legend>Weekdays</legend>{DAYS.map((day, index) => <Checkbox key={day} name="weekdays" value={index} defaultChecked={index === 0}>{day}</Checkbox>)}</fieldset>
          {refusalFor("weekdays")}
          <div className="field-grid"><div className="field"><label htmlFor={`${id}-local-time`}>Local start time</label><input id={`${id}-local-time`} className="input" type="time" name="local_time" required defaultValue="02:00" {...invalid("local_time")}/>{refusalFor("local_time")}</div><div className="field"><label htmlFor={`${id}-duration`}>Duration</label><DurationField id={`${id}-duration`} value={minutes} onChange={(next) => { setMinutes(next); setDurationError(""); }} units={MINUTES_TO_HOURS} label="Duration"
            inputProps={{ required: true, "aria-invalid": durationMessage ? true : undefined, "aria-describedby": durationMessage ? `${id}-duration-error` : undefined }}/>
            <input type="hidden" name="duration_minutes" value={minutes}/>
            {durationMessage ? <FieldError id={`${id}-duration-error`}>{durationMessage}</FieldError> : null}</div></div>
          <p id={`${id}-dst`}>Use an IANA timezone. A missing daylight-saving time is skipped; a repeated time starts once, at the earlier occurrence. Duration is elapsed minutes.</p>
        </> : <>
          <div className="field-grid">
            <div className="field"><label htmlFor={`${id}-starts-at`}>Start</label><input id={`${id}-starts-at`} className="input" type="datetime-local" name="starts_at" required {...invalid("starts_at", `${id}-zone`)}/>{refusalFor("starts_at")}</div>
            <div className="field"><label htmlFor={`${id}-ends-at`}>End</label><input id={`${id}-ends-at`} className="input" type="datetime-local" name="ends_at" required {...invalid("ends_at", `${id}-zone`)}/>{refusalFor("ends_at")}</div>
          </div>
          <p id={`${id}-zone`}>Times are in your time zone ({zone}). A time that happens twice when the clocks go back means the first of the two; a time the clocks skip cannot be chosen.</p>
        </>}
      </fieldset>
      <button className="button button--primary" type="submit" disabled={busy}>Schedule maintenance</button>
    </form> : null}
    <p ref={statusLine} tabIndex={-1} role="status">{busy ? "Saving maintenance…" : message}</p>
    {error ? <p role="alert">{error}</p> : null}
  </div>;
}
