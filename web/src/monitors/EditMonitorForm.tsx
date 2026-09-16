import { useId, useRef, useState } from "react";
import type { FormEvent } from "react";
import type { InventoryMonitor } from "./inventory";
import type { MonitorPatch } from "./inventoryApi";
import { tagsToText, textToTags } from "./tags";

/**
 * Editing an existing monitor.
 *
 * **It offers only what PATCH can actually change, and only what the list
 * endpoint gave us the current value of.** `GET /api/v1/monitors` returns name,
 * type, target, interval, timeout, enabled, capture_response, repeat_after_s,
 * tags and created_at — it does not return method, expected status, keyword,
 * headers, body, retries or ssl_warn_days. PATCH accepts all of those, but a
 * form that renders an input for a field whose current value it never received
 * has two bad options: show it blank, which invites the user to blank a real
 * setting, or guess a default and silently overwrite what is stored. Both are
 * the failure this product exists to avoid, so those fields are not offered
 * here at all. The remaining gap is named in the PR rather than hidden.
 *
 * **Target and type are read-only here for the same reason plus one more.**
 * Changing a target re-validates against the type and can fail in ways only a
 * preview can show; that path already exists, on the add form, with the preview
 * attached to it. An edit box that can silently point a monitor at a different
 * service without ever probing it is a worse offer than no box.
 *
 * Presentational: it owns its field values, the caller owns the network, so a
 * test drives saving and failure without a fetch.
 */

export type EditMonitorFormProps = {
  monitor: InventoryMonitor;
  /** Applies the patch. Rejecting shows the reason above the buttons. */
  onSave: (patch: MonitorPatch) => Promise<void>;
  onCancel?: () => void;
};

type Problem = {
  message: string;
  field: "name" | "interval" | "timeout" | "tags" | null;
} | null;

/** The ARIA a rejected input carries, so the message is read with the field. */
function fieldProblem(field: string, problem: Problem, ids: string) {
  if (problem === null || problem.field !== field) return {};
  return {
    "aria-invalid": true as const,
    "aria-describedby": `${ids}-${field}-error`,
  };
}

/** The message under one input, or nothing. */
function FieldError({
  field,
  problem,
  ids,
}: {
  field: string;
  problem: Problem;
  ids: string;
}) {
  if (problem === null || problem.field !== field) return null;
  return (
    <p className="add-field-error" id={`${ids}-${field}-error`} role="alert">
      {problem.message}
    </p>
  );
}

export function EditMonitorForm({
  monitor,
  onSave,
  onCancel,
}: EditMonitorFormProps) {
  const ids = useId();
  const [name, setName] = useState(monitor.name);
  const [intervalS, setIntervalS] = useState(String(monitor.intervalS));
  const [timeoutS, setTimeoutS] = useState(
    monitor.timeoutS === null ? "" : String(monitor.timeoutS),
  );
  /*
   * The tag field starts as text, and the text it starts as may not parse.
   *
   * `note: "a,b"` is a legal stored tag — the API carries an object precisely
   * so a value can contain punctuation — but rendered as `note:a,b` it reads
   * back as two entries, the second of which has no key. Re-parsing that on
   * every save refused every save, including a rename that never touched the
   * tags. So the initial text is remembered, and text that is still exactly
   * what we put there means "unchanged": the stored tags are used as they are
   * and nothing is parsed. Only text the user actually edited has to parse.
   */
  const initialTagText = tagsToText(monitor.tags);
  const [tagText, setTagText] = useState(initialTagText);
  const [saving, setSaving] = useState(false);
  /*
   * A rejection, and which control it is about.
   *
   * `field` is null for anything that is not about one input — "nothing
   * changed", or the server's own refusal — because pinning those under a box
   * tells the user to edit something that is not wrong. When it does name a
   * control, the message is rendered beside that control, wired to it with
   * `aria-describedby` and `aria-invalid`, and focus moves there: a message
   * sitting below the submit button is one a screen reader user has to go
   * looking for, and one a sighted user reads after the field they would have
   * to scroll back up to fix.
   */
  const [problem, setProblem] = useState<{
    message: string;
    field: "name" | "interval" | "timeout" | "tags" | null;
  } | null>(null);
  const nameRef = useRef<HTMLInputElement>(null);
  const intervalRef = useRef<HTMLInputElement>(null);
  const timeoutRef = useRef<HTMLInputElement>(null);
  const tagsRef = useRef<HTMLInputElement>(null);

  const reject = (
    message: string,
    field: "name" | "interval" | "timeout" | "tags" | null = null,
  ) => {
    setProblem({ message, field });
    // Focus moves to the control that was refused. Without it the caret can
    // sit on the submit button while the reason sits three fields above,
    // which for a keyboard or screen reader user is a form that silently
    // refused to do anything.
    if (field === "name") nameRef.current?.focus();
    else if (field === "interval") intervalRef.current?.focus();
    else if (field === "timeout") timeoutRef.current?.focus();
    else if (field === "tags") tagsRef.current?.focus();
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setProblem(null);

    const trimmed = name.trim();
    if (trimmed === "") {
      reject(
        "A monitor needs a name — it is how you find it in this list.",
        "name",
      );
      return;
    }
    const tagsUntouched = tagText === initialTagText;
    const tags = tagsUntouched ? monitor.tags : textToTags(tagText);
    if (tags === null) {
      reject(
        "Tags are written key:value, separated by commas — for example env:prod, team:payments.",
        "tags",
      );
      return;
    }
    const interval = Number(intervalS);
    if (!Number.isInteger(interval) || interval < 20 || interval > 86400) {
      reject("The interval is in seconds, between 20 and 86400.", "interval");
      return;
    }

    /*
     * Only what changed is sent.
     *
     * PATCH distinguishes "not sent" from "sent as the zero value" precisely so
     * a rename does not also reset the schedule, and sending every field on
     * every save would throw that guarantee away on the client side: two people
     * editing different fields of the same monitor would each overwrite the
     * other's change with a value they never touched.
     */
    const patch: MonitorPatch = {};
    if (trimmed !== monitor.name) patch.name = trimmed;
    if (interval !== monitor.intervalS) patch.interval_s = interval;
    if (monitor.timeoutS !== null) {
      const timeout = Number(timeoutS);
      if (!Number.isInteger(timeout) || timeout < 1 || timeout > 120) {
        reject("The timeout is in seconds, between 1 and 120.", "timeout");
        return;
      }
      if (timeout !== monitor.timeoutS) patch.timeout_s = timeout;
    }
    if (!tagsUntouched && tagsToText(tags) !== initialTagText) patch.tags = tags;

    if (Object.keys(patch).length === 0) {
      reject("Nothing changed, so nothing was sent.");
      return;
    }

    setSaving(true);
    void (async () => {
      try {
        await onSave(patch);
      } catch (error) {
        // The server's own sentence, and never pinned to a control: a 412 is
        // about the monitor having moved, not about anything the user typed.
        setProblem({
          message:
            error instanceof Error
              ? error.message
              : "the change could not be saved",
          field: null,
        });
      } finally {
        setSaving(false);
      }
    })();
  };

  return (
    <form className="add-form" onSubmit={submit}>
      <div className="add-field">
        <label className="add-label" htmlFor={`${ids}-name`}>
          Name
        </label>
        <input
          id={`${ids}-name`}
          ref={nameRef}
          className="add-input"
          value={name}
          onChange={(event) => setName(event.target.value)}
          {...fieldProblem("name", problem, ids)}
        />
        <FieldError field="name" problem={problem} ids={ids} />
      </div>

      <div className="add-grid">
        <div className="add-field">
          <label className="add-label" htmlFor={`${ids}-interval`}>
            Interval (seconds)
          </label>
          <input
            id={`${ids}-interval`}
            ref={intervalRef}
            className="add-input"
            inputMode="numeric"
            value={intervalS}
            onChange={(event) => setIntervalS(event.target.value)}
            {...fieldProblem("interval", problem, ids)}
          />
          <FieldError field="interval" problem={problem} ids={ids} />
        </div>

        {/* A push monitor has no timeout because SubGlance dials nothing, so
            the control is absent rather than disabled: a disabled input is a
            setting that looks temporarily unavailable, and this one does not
            exist. */}
        {monitor.timeoutS !== null && (
          <div className="add-field">
            <label className="add-label" htmlFor={`${ids}-timeout`}>
              Timeout (seconds)
            </label>
            <input
              id={`${ids}-timeout`}
              ref={timeoutRef}
              className="add-input"
              inputMode="numeric"
              value={timeoutS}
              onChange={(event) => setTimeoutS(event.target.value)}
              {...fieldProblem("timeout", problem, ids)}
            />
            <FieldError field="timeout" problem={problem} ids={ids} />
          </div>
        )}
      </div>

      <div className="add-field">
        <label className="add-label" htmlFor={`${ids}-tags`}>
          Tags
        </label>
        <input
          id={`${ids}-tags`}
          ref={tagsRef}
          className="add-input"
          value={tagText}
          onChange={(event) => setTagText(event.target.value)}
          placeholder="env:prod, team:payments"
          {...fieldProblem("tags", problem, ids)}
        />
        <FieldError field="tags" problem={problem} ids={ids} />
        <p className="add-help">
          Written key:value, separated by commas. Saving replaces the whole set,
          so a tag left out here is a tag removed.
        </p>
      </div>

      <p className="add-help">
        Target and check type are not editable here: changing either can only be
        verified by probing it, which the add form does with Test it. Method,
        expected status, keyword and headers are not shown because this screen
        never received their current values, and an input that starts blank
        would invite you to erase a setting you cannot see.
      </p>

      {/* Only what is NOT about a single control lands here. A message that
          names a field is rendered beside that field, above. */}
      {problem !== null && problem.field === null && (
        /* `role="alert"` because it appears in response to the user's own
           press and has to be heard without them going looking for it. */
        <p className="add-field-error" role="alert">
          {problem.message}
        </p>
      )}

      <div className="add-actions">
        <button
          type="submit"
          className="add-button add-button-primary"
          disabled={saving}
        >
          {saving ? "Saving…" : "Save changes"}
        </button>
        {onCancel !== undefined && (
          <button
            type="button"
            className="add-button add-button-quiet"
            onClick={onCancel}
          >
            Cancel
          </button>
        )}
      </div>
    </form>
  );
}
