import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent } from "react";
import { IconAlert } from "../components/icons";
import type { InventoryMonitor } from "./inventory";
import type { MonitorPatch } from "./inventoryApi";
import { RepeatAlertField } from "./RepeatAlertField";
import { validRepeat, REPEAT_ERROR } from "./repeat";
import { tagsToText, textToTags } from "./tags";
import { ApiError, describePreview, fingerprintPreview, previewCheck } from "./preview";
import type { PreviewRequest, PreviewState } from "./preview";
import { confirmLeave, registerLeaveGuard } from "../shell/leaveGuard";
import { TlsFloorField } from "./TlsFloorField";

export type EditMonitorFormProps = {
  /** Values and validator must come from the same detail response. */
  monitor: InventoryMonitor;
  onSave: (patch: MonitorPatch) => Promise<void>;
  onCancel?: () => void;
  /** Explicitly replaces the draft after a conflict; never retries a write. */
  onReload?: () => void;
};

type Problem = { message: string; field?: string } | null;
// These edits require a preview to save. TLS-only saves retain the existing
// contract, but a TLS change must still invalidate any preview already shown.
const CHECK_FIELDS = new Set(["target", "timeout_s", "method", "expected_status", "keyword", "keyword_mode", "follow_redirects", "headers", "body", "ssl_warn_days"]);
const LABELS: Record<string, string> = {
  name: "Name", target: "Target", interval_s: "Interval (seconds)", timeout_s: "Timeout (seconds)",
  method: "HTTP method", expected_status: "Expected status", keyword: "Keyword", keyword_mode: "Keyword rule",
  headers: "Headers (JSON)", body: "Request body", ssl_warn_days: "Certificate warning (days)",
  tags: "Tags", push_interval_s: "Should report every (seconds)", push_grace_s: "Allow it to be late by (seconds)",
};

/** Only settings actually read from the server become editable values. */
function valuesFor(monitor: InventoryMonitor): Record<string, string> {
  const values: Record<string, string> = { name: monitor.name, tags: tagsToText(monitor.tags) };
  if (monitor.repeatAfterS !== undefined) values.repeat_after_s = String(monitor.repeatAfterS);
  if (monitor.push) {
    values.push_interval_s = String(monitor.push.intervalS);
    values.push_grace_s = String(monitor.push.graceS);
  } else {
    // Absence is no opinion, not a pinned default. Keep it in the draft so
    // selecting no opinion can deliberately clear a previously stored floor.
    values.min_tls_version = monitor.minTlsVersion;
    values.target = monitor.target;
    values.interval_s = String(monitor.intervalS);
    if (monitor.timeoutS !== null) values.timeout_s = String(monitor.timeoutS);
    for (const [key, value] of Object.entries(monitor.checkSettings ?? {})) {
      if (key === "min_tls_version") continue; // The dedicated floor value also represents absence.
      values[key] = key === "headers" ? JSON.stringify(value, null, 2) : String(value);
    }
  }
  return values;
}

/** Shared by the inventory and detail drawers. Drafts, including request
 * credentials, stay in this mount only; the leave guard receives no values. */
export function EditMonitorForm({ monitor, onSave, onCancel, onReload }: EditMonitorFormProps) {
  const ids = useId();
  const [initial] = useState(() => valuesFor(monitor));
  const [values, setValues] = useState(initial);
  const [problem, setProblem] = useState<Problem>(null);
  const [preview, setPreview] = useState<PreviewState>({ phase: "idle" });
  const [saving, setSaving] = useState(false);
  const [conflict, setConflict] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  const dirty = useRef(false);
  const busy = useRef(false);
  const alive = useRef(true);
  const inFlight = useRef<AbortController | null>(null);
  const changed = JSON.stringify(values) !== JSON.stringify(initial);
  useEffect(() => { dirty.current = changed; }, [changed]);
  useEffect(() => {
    // API errors arrive while the fieldset is disabled. Focus after it unlocks.
    if (saving || !problem?.field) return;
    const control = problem.field === "repeat_after_s"
      ? form.current?.querySelector<HTMLElement>("[data-repeat-input]")
      : form.current?.elements.namedItem(problem.field === "min_tls_version" ? `${ids}-min-tls` : problem.field) as HTMLElement | null;
    const panel = control?.closest("details");
    if (panel) panel.open = true;
    control?.focus();
  }, [problem, saving, ids]);
  useEffect(() => {
    alive.current = true;
    const unregister = registerLeaveGuard({ element: () => form.current, dirty: () => dirty.current, discard: () => { dirty.current = false; } });
    return () => { alive.current = false; inFlight.current?.abort(); unregister(); };
  }, []);

  const update = (key: string, value: string) => {
    setValues((old) => ({ ...old, [key]: value }));
    if (!conflict) setProblem(null);
    if (CHECK_FIELDS.has(key) || key === "min_tls_version") {
      inFlight.current?.abort();
      setPreview({ phase: "idle" });
    }
  };
  const reject = (message: string, field?: string) => {
    const control = field === "repeat_after_s"
      ? form.current?.querySelector<HTMLElement>("[data-repeat-input]")
      : field ? form.current?.elements.namedItem(field === "min_tls_version" ? `${ids}-min-tls` : field) as HTMLElement | null : null;
    setProblem({ message, ...(control ? { field } : {}) });
    control?.focus();
  };
  const explain = (error: unknown) => reject(
    error instanceof ApiError && error.status === 429 && error.retryAfter !== null
      ? `${error.message} (about ${error.retryAfter}s)`
      : error instanceof Error ? error.message : "Could not reach SubGlance itself.",
    error instanceof ApiError ? error.field ?? undefined : undefined,
  );

  const validated = (): { patch: MonitorPatch; request: PreviewRequest } | null => {
    setProblem(null);
    if (!values.name.trim()) { reject("A monitor needs a name — it is how you find it in this list.", "name"); return null; }
    if (!monitor.push && !values.target.trim()) { reject("A target is required.", "target"); return null; }
    const tags = textToTags(values.tags);
    if (tags === null) { reject("Tags are written key:value, one per line — for example env:prod.", "tags"); return null; }
    const parsed: Record<string, unknown> = { ...values, name: values.name.trim(), tags };
    if (!monitor.push) parsed.target = values.target.trim();
    for (const [key, min, max] of [["interval_s", 20, 86400], ["timeout_s", 1, 120], ["push_interval_s", 60, 2592000], ["push_grace_s", 0, 2592000], ["ssl_warn_days", 1, 365]] as const) {
      if (!(key in values)) continue;
      const number = Number(values[key]);
      if (values[key].trim() === "" || !Number.isInteger(number) || number < min || number > max) {
        reject(`${LABELS[key]} must be between ${min} and ${max}.`, key); return null;
      }
      parsed[key] = number;
    }
    if ("repeat_after_s" in values) {
      if (!validRepeat(values.repeat_after_s)) { reject(REPEAT_ERROR, "repeat_after_s"); return null; }
      parsed.repeat_after_s = Number(values.repeat_after_s);
    }
    if ("follow_redirects" in values) parsed.follow_redirects = values.follow_redirects === "true";
    if ("headers" in values) {
      try {
        const headers: unknown = JSON.parse(values.headers);
        if (headers === null || typeof headers !== "object" || Array.isArray(headers) || !Object.values(headers).every((v) => typeof v === "string")) throw new Error();
        parsed.headers = headers;
      } catch { reject("Headers must be a JSON object with text values, or {} to clear them.", "headers"); return null; }
    }
    const patch: MonitorPatch = {};
    for (const key of Object.keys(values)) {
      if (values[key] !== initial[key]) Object.assign(patch, { [key]: parsed[key] });
    }
    // Normalised no-ops need no write; zero, false and empty remain explicit.
    if (parsed.name === monitor.name) delete patch.name;
    if (tagsToText(tags) === initial.tags) delete patch.tags;
    const request: PreviewRequest = { ...monitor.checkSettings, type: monitor.type, target: values.target?.trim() ?? "" };
    for (const key of CHECK_FIELDS) if (key in parsed) Object.assign(request, { [key]: parsed[key] });
    // Preview has no stored floor to clear: omission asks for the default.
    // PATCH, in contrast, keeps the explicit empty string from the draft.
    if (values.min_tls_version) request.min_tls_version = values.min_tls_version;
    else delete request.min_tls_version;
    return { patch, request };
  };

  const runPreview = () => {
    const draft = validated(); if (!draft || saving || conflict) return;
    inFlight.current?.abort();
    const controller = new AbortController(); inFlight.current = controller;
    setPreview({ phase: "checking" });
    void previewCheck(draft.request, controller.signal).then((result) => {
      if (!controller.signal.aborted && alive.current) setPreview({ phase: "done", result, request: fingerprintPreview(draft.request) });
    }).catch((error: unknown) => {
      if (controller.signal.aborted || !alive.current) return;
      setPreview({ phase: "idle" }); explain(error);
    });
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (busy.current || conflict) return;
    const draft = validated(); if (!draft) return;
    if (!Object.keys(draft.patch).length) { reject("Nothing changed, so nothing was sent."); return; }
    const changesProbe = Object.keys(draft.patch).some((key) => CHECK_FIELDS.has(key));
    if (!monitor.push && changesProbe && (preview.phase !== "done" || preview.request !== fingerprintPreview(draft.request))) {
      reject("Press Test it before saving changed check settings. A Down result can still be saved.", "target"); return;
    }
    if (draft.patch.target !== undefined && preview.phase === "done") draft.patch.target = preview.result.target;
    busy.current = true; setSaving(true);
    void onSave(draft.patch).then(() => { dirty.current = false; }).catch((error: unknown) => {
      if (!alive.current) return;
      if (error instanceof ApiError && error.status === 412) {
        setConflict(true);
        reject("This monitor changed while you were editing, possibly by someone else. Nothing was overwritten. Your draft is kept here; reload the latest settings to start again.");
      } else explain(error);
    }).finally(() => { busy.current = false; if (alive.current) setSaving(false); });
  };

  const field = (key: string, multiline = false) => {
    if (!(key in values)) return null;
    const props = { id: `${ids}-${key}`, name: key, className: "add-input", value: values[key],
      onChange: (event: { target: { value: string } }) => update(key, event.target.value),
      "aria-invalid": problem?.field === key ? true as const : undefined,
      "aria-describedby": problem?.field === key ? `${ids}-error` : undefined };
    return <div className="add-field" key={key}>
      <label className="add-label" htmlFor={props.id}>{LABELS[key]}</label>
      {multiline ? <textarea {...props} rows={3} spellCheck={false} /> : <input {...props} autoComplete="off" />}
      {problem?.field === key && <p id={`${ids}-error`} role="alert" className="add-field-error"><IconAlert />{problem.message}</p>}
    </div>;
  };
  return <form ref={form} className="add-form edit-form" onSubmit={submit} noValidate aria-label="Edit monitor settings">
    <fieldset className="contents" disabled={saving}>
      {field("name")}
      <p className="add-help">Check type: {monitor.type.toUpperCase()}. Check type stays fixed to preserve this monitor’s identity and push token.</p>
      {field("target")}
      <div className="add-grid">{field("interval_s")}{field("timeout_s")}{field("push_interval_s")}{field("push_grace_s")}</div>
      {!monitor.push && <>
        {field("method")}{field("expected_status")}{field("keyword")}{field("keyword_mode")}
        {"follow_redirects" in values && <div className="add-field">
          <label className="add-help"><input type="checkbox" name="follow_redirects" checked={values.follow_redirects === "true"} onChange={(event) => update("follow_redirects", String(event.target.checked))}
            aria-invalid={problem?.field === "follow_redirects" ? true : undefined}
            aria-describedby={problem?.field === "follow_redirects" ? `${ids}-error` : undefined} /> Follow redirects</label>
          {problem?.field === "follow_redirects" && <p id={`${ids}-error`} role="alert" className="add-field-error"><IconAlert />{problem.message}</p>}
        </div>}
        {field("headers", true)}{field("body", true)}{field("ssl_warn_days")}
        <details className="add-advanced">
          <summary className="add-summary">Advanced options</summary>
          <div className="add-grid">
            <TlsFloorField id={`${ids}-min-tls`} value={values.min_tls_version}
              onChange={(value) => update("min_tls_version", value)}
              invalid={problem?.field === "min_tls_version"}
              errorId={problem?.field === "min_tls_version" ? `${ids}-error` : undefined}>
              {problem?.field === "min_tls_version" && <p id={`${ids}-error`} role="alert" className="add-field-error"><IconAlert />{problem.message}</p>}
            </TlsFloorField>
          </div>
        </details>
        <p className="add-help">Test it probes these settings without saving, recording history or sending alerts. A failed check can still be saved.</p>
      </>}
      {field("tags", true)}
      <p className="add-help">One key:value per line — a value may contain commas and colons. Saving replaces the whole set, so a tag left out here is a tag removed.</p>
      {"repeat_after_s" in values ? <RepeatAlertField value={values.repeat_after_s} onChange={(value) => update("repeat_after_s", value)} error={problem?.field === "repeat_after_s" ? problem.message : undefined} /> : <p className="add-help">Repeat alert settings unavailable. Reload to read the current value.</p>}
    </fieldset>
    {problem && !problem.field && <p className="add-field-error" role="alert"><IconAlert />{problem.message}</p>}
    <div role="status" aria-live="polite">
      {preview.phase === "checking" && <p className="add-result">Checking…</p>}
      {preview.phase === "done" && <p className={`add-result ${preview.result.ok ? "add-result-good" : "add-result-bad"}`}>{describePreview(preview.result)}</p>}
    </div>
    {saving && <p className="add-help">A save in progress may still complete if you close this form.</p>}
    <div className="add-actions">
      {!monitor.push && <button className="add-button" type="button" onClick={runPreview} disabled={saving || conflict}>{preview.phase === "checking" ? "Testing…" : "Test it"}</button>}
      <button className="add-button add-button-primary" type="submit" disabled={saving || conflict}>{saving ? "Saving…" : "Save changes"}</button>
      {conflict && onReload && <button className="add-button" type="button" onClick={onReload}>Reload latest settings</button>}
      {onCancel && <button className="add-button add-button-quiet" type="button" onClick={() => { if (confirmLeave(form.current)) onCancel(); }}>Cancel</button>}
    </div>
  </form>;
}
