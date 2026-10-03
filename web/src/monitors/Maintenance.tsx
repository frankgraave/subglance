import { useId, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { apiJSON, apiPost, apiRequest } from "../api/http";
import { Checkbox } from "../components/Choice";
import { IconAlert } from "../components/icons";
import { windowCovers, type MaintenanceMonitor } from "./maintenanceScope";
import { DurationField } from "./DurationField";
import { MINUTES_TO_HOURS } from "./duration";

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
      setMessage(""); setError(""); setDurationError("The duration must be between 1 min and 24 h, in whole minutes."); return;
    }
    setDurationError("");
    const body = { name: value("name"),
      ...(scope === "monitor" ? { monitor_id: Number(value("monitor_id")) } : { tag_key: value("tag_key"), tag_value: value("tag_value") }),
      ...(weekly ? { timezone: value("timezone"), local_time: value("local_time"), weekdays: data.getAll("weekdays").map(Number), duration_minutes: Number(value("duration_minutes")) }
        : { starts_at: `${value("starts_at")}:00Z`, ends_at: `${value("ends_at")}:00Z` }),
    };
    setBusy(true); setError(""); setMessage("");
    try { await apiPost("/api/v1/maintenance", body); await refresh(); setMessage("Maintenance scheduled."); }
    catch (e) { setError(e instanceof Error ? e.message : "Could not schedule maintenance."); }
    finally { setBusy(false); }
  }
  async function cancel(window: Window) {
    if (busy) return;
    setBusy(true); setError(""); setMessage("");
    try { await apiRequest(`/api/v1/maintenance/${window.id}`, { method: "DELETE" }); await refresh(); setMessage(`Cancelled ${window.name}. Recorded history is unchanged.`); }
    catch (e) { setError(e instanceof Error ? e.message : "Could not cancel maintenance."); }
    finally { setBusy(false); }
  }
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
    {canWrite ? <form className="form-column" onSubmit={(event) => void submit(event)} aria-label="Schedule maintenance">
      <fieldset disabled={busy} className="maintenance-fields">
        <label className="field">Name<input className="input" name="name" required maxLength={120}/></label>
        <label className="field">Applies to<select className="input" name="scope" value={scope} onChange={(e) => setScope(e.target.value)}><option value="monitor">One monitor</option><option value="tag">Tag group</option></select></label>
        {scope === "monitor" ? <label className="field">Monitor<select className="input" name="monitor_id" required defaultValue={focus?.id ?? ""}><option value="">Choose a monitor</option>{monitors.map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}</select></label>
          : <div className="field-grid"><label className="field">Tag key<input className="input" name="tag_key" required placeholder="env"/></label><label className="field">Tag value<input className="input" name="tag_value" required placeholder="prod"/></label></div>}
        <label className="field">Schedule<select className="input" name="schedule" value={weekly ? "weekly" : "once"} onChange={(e) => setWeekly(e.target.value === "weekly")}><option value="once">One-off</option><option value="weekly">Weekly</option></select></label>
        {weekly ? <>
          <label className="field">Timezone<input className="input" name="timezone" required defaultValue="UTC" aria-describedby={`${id}-dst`} placeholder="Europe/Amsterdam"/></label>
          <fieldset className="maintenance-days"><legend>Weekdays</legend>{DAYS.map((day, index) => <Checkbox key={day} name="weekdays" value={index} defaultChecked={index === 0}>{day}</Checkbox>)}</fieldset>
          <div className="field-grid"><label className="field">Local start time<input className="input" type="time" name="local_time" required defaultValue="02:00"/></label><div className="field"><label htmlFor={`${id}-duration`}>Duration</label><DurationField id={`${id}-duration`} value={minutes} onChange={(next) => { setMinutes(next); setDurationError(""); }} units={MINUTES_TO_HOURS} label="Duration"
            inputProps={{ required: true, "aria-invalid": durationError ? true : undefined, "aria-describedby": durationError ? `${id}-duration-error` : undefined }}/>
            <input type="hidden" name="duration_minutes" value={minutes}/>
            {durationError ? <p id={`${id}-duration-error`} role="alert" className="field-error"><IconAlert />{durationError}</p> : null}</div></div>
          <p id={`${id}-dst`}>Use an IANA timezone. A missing daylight-saving time is skipped; a repeated time starts once, at the earlier occurrence. Duration is elapsed minutes.</p>
        </> : <div className="field-grid"><label className="field">Start (UTC)<input className="input" type="datetime-local" name="starts_at" required/></label><label className="field">End (UTC)<input className="input" type="datetime-local" name="ends_at" required/></label></div>}
      </fieldset>
      <button className="button button--primary" type="submit" disabled={busy}>Schedule maintenance</button>
    </form> : null}
    <p role="status">{busy ? "Saving maintenance…" : message}</p>
    {error ? <p role="alert">{error}</p> : null}
  </div>;
}
