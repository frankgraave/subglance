import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, apiJSON, apiPost, apiRequest } from "../api/http";
import { Checkbox } from "../components/Choice";
import { windowCovers, type MaintenanceMonitor } from "./maintenanceScope";
import { DurationField } from "./DurationField";
import { MINUTES_TO_HOURS } from "./duration";
import { Select } from "../components/Select";
import { FieldError } from "../components/FieldError";
import { refusalInput } from "./maintenanceFields";

type Window = {
  id: number; name: string; monitor_id?: number; tag_key?: string; tag_value?: string;
  starts_at?: string; ends_at?: string; timezone?: string; weekdays?: number[];
  local_time?: string; duration_minutes?: number; active: boolean;
};
const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
const KEY = ["maintenance"];

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
  const shown = focus === undefined ? windows.data : windows.data?.filter((window) => windowCovers(window, focus));
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
    const body = { name: value("name"),
      ...(scope === "monitor" ? { monitor_id: Number(value("monitor_id")) } : { tag_key: value("tag_key"), tag_value: value("tag_value") }),
      ...(weekly ? { timezone: value("timezone"), local_time: value("local_time"), weekdays: data.getAll("weekdays").map(Number), duration_minutes: Number(value("duration_minutes")) }
        : { starts_at: `${value("starts_at")}:00Z`, ends_at: `${value("ends_at")}:00Z` }),
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
  async function cancel(window: Window) {
    if (busy) return;
    setBusy(true); setError(""); setMessage("");
    try { await apiRequest(`/api/v1/maintenance/${window.id}`, { method: "DELETE" }); await refresh(); setMessage(`Cancelled ${window.name}. Recorded history is unchanged.`); }
    catch (e) { setError(e instanceof Error ? e.message : "Could not cancel maintenance."); }
    finally { setBusy(false); }
  }
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
    <ul className="maintenance-list">{shown?.map((window) => <li key={window.id}>
      <strong>{window.name}</strong> — {window.active ? "Active now" : "Not active now"}
      <p>{window.monitor_id ? monitors.find((m) => m.id === String(window.monitor_id))?.name ?? `Monitor ${window.monitor_id}` : `${window.tag_key}:${window.tag_value}`}</p>
      <p>{window.timezone ? `${window.weekdays?.map((day) => DAYS[day]).join(", ")} at ${window.local_time} (${window.timezone}), ${window.duration_minutes} minutes`
        : `${window.starts_at} → ${window.ends_at}`}</p>
      {canWrite ? <button className="button" type="button" disabled={busy} aria-label={`Cancel maintenance ${window.name}`} onClick={() => void cancel(window)}>Cancel maintenance</button> : null}
    </li>)}</ul>
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
          <div className="field"><label htmlFor={`${id}-timezone`}>Timezone</label><input id={`${id}-timezone`} className="input" name="timezone" required defaultValue="UTC" placeholder="Europe/Amsterdam" {...invalid("timezone", `${id}-dst`)}/>{refusalFor("timezone")}</div>
          {/* The group is what a weekdays refusal is about, not any one box in it. */}
          <fieldset className="maintenance-days" {...invalid("weekdays")}><legend>Weekdays</legend>{DAYS.map((day, index) => <Checkbox key={day} name="weekdays" value={index} defaultChecked={index === 0}>{day}</Checkbox>)}</fieldset>
          {refusalFor("weekdays")}
          <div className="field-grid"><div className="field"><label htmlFor={`${id}-local-time`}>Local start time</label><input id={`${id}-local-time`} className="input" type="time" name="local_time" required defaultValue="02:00" {...invalid("local_time")}/>{refusalFor("local_time")}</div><div className="field"><label htmlFor={`${id}-duration`}>Duration</label><DurationField id={`${id}-duration`} value={minutes} onChange={(next) => { setMinutes(next); setDurationError(""); }} units={MINUTES_TO_HOURS} label="Duration"
            inputProps={{ required: true, "aria-invalid": durationMessage ? true : undefined, "aria-describedby": durationMessage ? `${id}-duration-error` : undefined }}/>
            <input type="hidden" name="duration_minutes" value={minutes}/>
            {durationMessage ? <FieldError id={`${id}-duration-error`}>{durationMessage}</FieldError> : null}</div></div>
          <p id={`${id}-dst`}>Use an IANA timezone. A missing daylight-saving time is skipped; a repeated time starts once, at the earlier occurrence. Duration is elapsed minutes.</p>
        </> : <div className="field-grid">
          <div className="field"><label htmlFor={`${id}-starts-at`}>Start (UTC)</label><input id={`${id}-starts-at`} className="input" type="datetime-local" name="starts_at" required {...invalid("starts_at")}/>{refusalFor("starts_at")}</div>
          <div className="field"><label htmlFor={`${id}-ends-at`}>End (UTC)</label><input id={`${id}-ends-at`} className="input" type="datetime-local" name="ends_at" required {...invalid("ends_at")}/>{refusalFor("ends_at")}</div>
        </div>}
      </fieldset>
      <button className="button button--primary" type="submit" disabled={busy}>Schedule maintenance</button>
    </form> : null}
    <p role="status">{busy ? "Saving maintenance…" : message}</p>
    {error ? <p role="alert">{error}</p> : null}
  </div>;
}
