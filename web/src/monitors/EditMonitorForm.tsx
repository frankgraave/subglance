import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent } from "react";
import { IconAlert } from "../components/icons";
import { Checkbox } from "../components/Choice";
import type { InventoryMonitor } from "./inventory";
import type { MonitorPatch } from "./inventoryApi";
import { RepeatAlertField } from "./RepeatAlertField";
import { validRepeat, REPEAT_ERROR } from "./repeat";
import { tagsToText, textToTags } from "./tags";
import { ApiError, describePreview, fingerprintPreview, previewCheck } from "./preview";
import type { PreviewRequest, PreviewState } from "./preview";
import { confirmLeave, registerLeaveGuard } from "../shell/leaveGuard";
import { TlsFloorField } from "./TlsFloorField";
import { JSON_HELP, JSON_OPERATORS, assertionFrom, expectedProblem, expectedText } from "./jsonAssertion";

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
const CHECK_FIELDS = new Set(["target", "timeout_s", "method", "expected_status", "keyword", "keyword_mode", "follow_redirects", "headers", "body", "ssl_warn_days", "json_assertion"]);
/**
 * The control a server problem field belongs to. `json_assertion.expected`
 * is the `json_expected` control; the bare `json_assertion` (wrong monitor
 * type, malformed object) points at the path, where the assertion starts.
 */
const controlFor = (field: string) =>
  field.startsWith("json_assertion") ? `json_${field.split(".")[1] ?? "path"}` : field;
const LABELS: Record<string, string> = {
  name: "Name", target: "Target", interval_s: "Interval (seconds)", timeout_s: "Timeout (seconds)",
  method: "HTTP method", expected_status: "Expected status", keyword: "Keyword", keyword_mode: "Keyword rule",
  headers: "Headers (JSON)", body: "Request body", ssl_warn_days: "Certificate warning (days)",
  recovery_threshold: "Passing checks to recover",
  json_path: "JSON field", json_operator: "Must", json_expected: "Value",
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
    // Push monitors close on one report whatever is stored, so the field is
    // only offered where it changes something.
    if (monitor.recoveryThreshold !== undefined) values.recovery_threshold = String(monitor.recoveryThreshold);
    // Editable only when the detail read said what is stored, null included:
    // an unknown assertion shown as empty would be removed by the first save.
    const assertion = monitor.jsonAssertion;
    if (monitor.type === "http" && assertion !== undefined) {
      values.json_path = assertion?.path ?? "";
      values.json_operator = assertion?.operator ?? "equals";
      values.json_expected = expectedText(assertion?.expected);
    }
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
    if (CHECK_FIELDS.has(key) || key === "min_tls_version" || key.startsWith("json_")) {
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
  const explain = (error: unknown) => {
    const blamed = error instanceof ApiError ? error.field ?? undefined : undefined;
    reject(
      error instanceof ApiError && error.status === 429 && error.retryAfter !== null
        ? `${error.message} (about ${error.retryAfter}s)`
        : error instanceof Error ? error.message : "Could not reach SubGlance itself.",
      blamed && controlFor(blamed),
    );
  };

  const validated = (): { patch: MonitorPatch; request: PreviewRequest } | null => {
    setProblem(null);
    if (!values.name.trim()) { reject("A monitor needs a name — it is how you find it in this list.", "name"); return null; }
    if (!monitor.push && !values.target.trim()) { reject("A target is required.", "target"); return null; }
    const tags = textToTags(values.tags);
    if (tags === null) { reject("Tags are written key:value, one per line — for example env:prod.", "tags"); return null; }
    const parsed: Record<string, unknown> = { ...values, name: values.name.trim(), tags };
    if (!monitor.push) parsed.target = values.target.trim();
    for (const [key, min, max] of [["interval_s", 20, 86400], ["recovery_threshold", 1, 10], ["timeout_s", 1, 120], ["push_interval_s", 60, 2592000], ["push_grace_s", 0, 2592000], ["ssl_warn_days", 1, 365]] as const) {
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
    if ("json_path" in values) {
      const unsendable = values.json_path.trim() !== "" && values.json_operator !== "exists" ? expectedProblem(values.json_expected) : null;
      if (unsendable !== null) { reject(unsendable, "json_expected"); return null; }
      parsed.json_assertion = assertionFrom(values.json_path, values.json_operator, values.json_expected);
    }
    const patch: MonitorPatch = {};
    for (const key of Object.keys(values)) {
      // The three json_ controls are one API field: a change to any of them
      // sends it whole, and a cleared path sends null, which removes it.
      const wire = key.startsWith("json_") ? "json_assertion" : key;
      if (values[key] !== initial[key]) Object.assign(patch, { [wire]: parsed[wire] });
    }
    // Normalised no-ops need no write; zero, false and empty remain explicit.
    if (parsed.name === monitor.name) delete patch.name;
    if (tagsToText(tags) === initial.tags) delete patch.tags;
    const request: PreviewRequest = { ...monitor.checkSettings, type: monitor.type, target: values.target?.trim() ?? "" };
    for (const key of CHECK_FIELDS) if (key in parsed) Object.assign(request, { [key]: parsed[key] });
    // Preview has no stored assertion to clear: absence means none.
    if (request.json_assertion === null) delete request.json_assertion;
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

  /** A text input, a textarea, or with `options` a select labelled by its values. */
  const field = (key: string, multiline = false, options?: readonly string[]) => {
    if (!(key in values)) return null;
    const props = { id: `${ids}-${key}`, name: key, className: "input", value: values[key],
      onChange: (event: { target: { value: string } }) => update(key, event.target.value),
      "aria-invalid": problem?.field === key ? true as const : undefined,
      "aria-describedby": problem?.field === key ? `${ids}-error` : undefined };
    return <div className="field" key={key}>
      <label className="field-label" htmlFor={props.id}>{LABELS[key]}</label>
      {options ? <select {...props}>{options.map((op) => <option key={op} value={op}>{op.replace("_", " ")}</option>)}</select>
        : multiline ? <textarea {...props} rows={3} spellCheck={false} /> : <input {...props} autoComplete="off" />}
      {problem?.field === key && <p id={`${ids}-error`} role="alert" className="field-error"><IconAlert />{problem.message}</p>}
    </div>;
  };
  return <form ref={form} className="form-column edit-form" onSubmit={submit} noValidate aria-label="Edit monitor settings">
    <fieldset className="contents" disabled={saving}>
      {field("name")}
      <p className="field-help">Check type: {monitor.type.toUpperCase()}. Check type stays fixed to preserve this monitor’s identity and push token.</p>
      {field("target")}
      <div className="field-grid">{field("interval_s")}{field("timeout_s")}{field("recovery_threshold")}{field("push_interval_s")}{field("push_grace_s")}</div>
      {"recovery_threshold" in values && <p className="field-help">An open incident closes, and “resolved” is sent, only after this many passing checks in a row. 1 closes on the first pass.</p>}
      {!monitor.push && <>
        {field("method")}{field("expected_status")}{field("keyword")}{field("keyword_mode")}
        {"follow_redirects" in values && <div className="field">
          <Checkbox name="follow_redirects" checked={values.follow_redirects === "true"} onChange={(event) => update("follow_redirects", String(event.target.checked))}
            aria-invalid={problem?.field === "follow_redirects" ? true : undefined}
            aria-describedby={problem?.field === "follow_redirects" ? `${ids}-error` : undefined}>Follow redirects</Checkbox>
          {problem?.field === "follow_redirects" && <p id={`${ids}-error`} role="alert" className="field-error"><IconAlert />{problem.message}</p>}
        </div>}
        {field("headers", true)}{field("body", true)}{field("ssl_warn_days")}
        {"json_path" in values && <>
          <div className="field-grid">
            {field("json_path")}{field("json_operator", false, JSON_OPERATORS)}
            {values.json_operator !== "exists" && field("json_expected")}
          </div>
          <p className="field-help">{JSON_HELP}</p>
        </>}
        <details className="add-advanced">
          <summary className="add-summary">Advanced options</summary>
          <div className="field-grid">
            <TlsFloorField id={`${ids}-min-tls`} value={values.min_tls_version}
              onChange={(value) => update("min_tls_version", value)}
              invalid={problem?.field === "min_tls_version"}
              errorId={problem?.field === "min_tls_version" ? `${ids}-error` : undefined}>
              {problem?.field === "min_tls_version" && <p id={`${ids}-error`} role="alert" className="field-error"><IconAlert />{problem.message}</p>}
            </TlsFloorField>
          </div>
        </details>
        <p className="field-help">Test it probes these settings without saving, recording history or sending alerts. A failed check can still be saved.</p>
      </>}
      {field("tags", true)}
      <p className="field-help">One key:value per line — a value may contain commas and colons. Saving replaces the whole set, so a tag left out here is a tag removed.</p>
      {"repeat_after_s" in values ? <RepeatAlertField value={values.repeat_after_s} onChange={(value) => update("repeat_after_s", value)} error={problem?.field === "repeat_after_s" ? problem.message : undefined} /> : <p className="field-help">Repeat alert settings unavailable. Reload to read the current value.</p>}
    </fieldset>
    {problem && !problem.field && <p className="field-error" role="alert"><IconAlert />{problem.message}</p>}
    <div role="status" aria-live="polite">
      {preview.phase === "checking" && <p className="result">Checking…</p>}
      {preview.phase === "done" && <p className={`result ${preview.result.ok ? "result--good" : "result--bad"}`}>{describePreview(preview.result)}</p>}
    </div>
    {saving && <p className="field-help">A save in progress may still complete if you close this form.</p>}
    <div className="button-row">
      {!monitor.push && <button className="button" type="button" onClick={runPreview} disabled={saving || conflict}>{preview.phase === "checking" ? "Testing…" : "Test it"}</button>}
      <button className="button button--primary" type="submit" disabled={saving || conflict}>{saving ? "Saving…" : "Save changes"}</button>
      {conflict && onReload && <button className="button" type="button" onClick={onReload}>Reload latest settings</button>}
      {onCancel && <button className="button button--quiet" type="button" onClick={() => { if (confirmLeave(form.current)) onCancel(); }}>Cancel</button>}
    </div>
  </form>;
}
