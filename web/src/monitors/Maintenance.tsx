import { useId, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { apiJSON, apiPost, apiRequest } from "../api/http";
import { Card } from "../components/Card";
import type { InventoryMonitor } from "./inventory";

type Window = {
  id: number; name: string; monitor_id?: number; tag_key?: string; tag_value?: string;
  starts_at?: string; ends_at?: string; timezone?: string; weekdays?: number[];
  local_time?: string; duration_minutes?: number; active: boolean;
};
const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
const KEY = ["maintenance"];

export function Maintenance({ monitors, canWrite }: { monitors: readonly InventoryMonitor[]; canWrite: boolean }) {
  const [open, setOpen] = useState(false);
  return <Card title="Scheduled maintenance" className="maintenance">
    <details onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary className="add-summary">Manage scheduled maintenance</summary>
      {open ? <MaintenanceManager monitors={monitors} canWrite={canWrite} /> : null}
    </details>
  </Card>;
}

function MaintenanceManager({ monitors, canWrite }: { monitors: readonly InventoryMonitor[]; canWrite: boolean }) {
  const queryClient = useQueryClient();
  const id = useId();
  const [scope, setScope] = useState("monitor");
  const [weekly, setWeekly] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const windows = useQuery({ queryKey: KEY, queryFn: async ({ signal }) => {
    const data = await apiJSON<{ maintenance: Window[] }>("/api/v1/maintenance", { signal });
    if (!Array.isArray(data.maintenance)) throw new Error("The server did not return maintenance windows.");
    return data.maintenance;
  }, refetchInterval: 15_000 });
  const refresh = async () => { await Promise.all([
    queryClient.invalidateQueries({ queryKey: KEY }),
    queryClient.invalidateQueries({ queryKey: ["monitors"] }),
  ]); };
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) return;
    const form = event.currentTarget;
    const data = new FormData(form);
    const value = (key: string) => String(data.get(key) ?? "");
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
    {windows.error ? <p role="alert">Could not load maintenance. Previously loaded schedules may be out of date. {windows.error.message}</p> : null}
    {windows.data?.length === 0 ? <p>No maintenance windows scheduled.</p> : null}
    <ul className="maintenance-list">{windows.data?.map((window) => <li key={window.id}>
      <strong>{window.name}</strong> — {window.active ? "Active now" : "Not active now"}
      <p>{window.monitor_id ? monitors.find((m) => m.id === String(window.monitor_id))?.name ?? `Monitor ${window.monitor_id}` : `${window.tag_key}:${window.tag_value}`}</p>
      <p>{window.timezone ? `${window.weekdays?.map((day) => DAYS[day]).join(", ")} at ${window.local_time} (${window.timezone}), ${window.duration_minutes} minutes`
        : `${window.starts_at} → ${window.ends_at}`}</p>
      {canWrite ? <button className="add-button" type="button" disabled={busy} aria-label={`Cancel maintenance ${window.name}`} onClick={() => void cancel(window)}>Cancel maintenance</button> : null}
    </li>)}</ul>
    {canWrite ? <form className="add-form" onSubmit={(event) => void submit(event)} aria-label="Schedule maintenance">
      <fieldset disabled={busy} className="maintenance-fields">
        <label className="add-field">Name<input className="add-input" name="name" required maxLength={120}/></label>
        <label className="add-field">Applies to<select className="add-input" name="scope" value={scope} onChange={(e) => setScope(e.target.value)}><option value="monitor">One monitor</option><option value="tag">Tag group</option></select></label>
        {scope === "monitor" ? <label className="add-field">Monitor<select className="add-input" name="monitor_id" required><option value="">Choose a monitor</option>{monitors.map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}</select></label>
          : <div className="add-grid"><label className="add-field">Tag key<input className="add-input" name="tag_key" required placeholder="env"/></label><label className="add-field">Tag value<input className="add-input" name="tag_value" required placeholder="prod"/></label></div>}
        <label className="add-field">Schedule<select className="add-input" name="schedule" value={weekly ? "weekly" : "once"} onChange={(e) => setWeekly(e.target.value === "weekly")}><option value="once">One-off</option><option value="weekly">Weekly</option></select></label>
        {weekly ? <>
          <label className="add-field">Timezone<input className="add-input" name="timezone" required defaultValue="UTC" aria-describedby={`${id}-dst`} placeholder="Europe/Amsterdam"/></label>
          <fieldset className="maintenance-days"><legend>Weekdays</legend>{DAYS.map((day, index) => <label key={day}><input type="checkbox" name="weekdays" value={index} defaultChecked={index === 0}/>{day}</label>)}</fieldset>
          <div className="add-grid"><label className="add-field">Local start time<input className="add-input" type="time" name="local_time" required defaultValue="02:00"/></label><label className="add-field">Duration (minutes)<input className="add-input" type="number" name="duration_minutes" min={1} max={1440} required defaultValue={60}/></label></div>
          <p id={`${id}-dst`}>Use an IANA timezone. A missing daylight-saving time is skipped; a repeated time starts once, at the earlier occurrence. Duration is elapsed minutes.</p>
        </> : <div className="add-grid"><label className="add-field">Start (UTC)<input className="add-input" type="datetime-local" name="starts_at" required/></label><label className="add-field">End (UTC)<input className="add-input" type="datetime-local" name="ends_at" required/></label></div>}
      </fieldset>
      <button className="add-button add-button-primary" type="submit" disabled={busy}>Schedule maintenance</button>
    </form> : null}
    <p role="status">{busy ? "Saving maintenance…" : message}</p>
    {error ? <p role="alert">{error}</p> : null}
  </div>;
}
