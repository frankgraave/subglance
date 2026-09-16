import { useId, useState } from "react";
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
  const [tagText, setTagText] = useState(tagsToText(monitor.tags));
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setProblem(null);

    const trimmed = name.trim();
    if (trimmed === "") {
      setProblem("A monitor needs a name — it is how you find it in this list.");
      return;
    }
    const tags = textToTags(tagText);
    if (tags === null) {
      setProblem(
        "Tags are written key:value, separated by commas — for example env:prod, team:payments.",
      );
      return;
    }
    const interval = Number(intervalS);
    if (!Number.isInteger(interval) || interval < 20 || interval > 86400) {
      setProblem("The interval is in seconds, between 20 and 86400.");
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
        setProblem("The timeout is in seconds, between 1 and 120.");
        return;
      }
      if (timeout !== monitor.timeoutS) patch.timeout_s = timeout;
    }
    if (tagsToText(tags) !== tagsToText(monitor.tags)) patch.tags = tags;

    if (Object.keys(patch).length === 0) {
      setProblem("Nothing changed, so nothing was sent.");
      return;
    }

    setSaving(true);
    void (async () => {
      try {
        await onSave(patch);
      } catch (error) {
        setProblem(
          error instanceof Error
            ? error.message
            : "the change could not be saved",
        );
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
          className="add-input"
          value={name}
          onChange={(event) => setName(event.target.value)}
        />
      </div>

      <div className="add-grid">
        <div className="add-field">
          <label className="add-label" htmlFor={`${ids}-interval`}>
            Interval (seconds)
          </label>
          <input
            id={`${ids}-interval`}
            className="add-input"
            inputMode="numeric"
            value={intervalS}
            onChange={(event) => setIntervalS(event.target.value)}
          />
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
              className="add-input"
              inputMode="numeric"
              value={timeoutS}
              onChange={(event) => setTimeoutS(event.target.value)}
            />
          </div>
        )}
      </div>

      <div className="add-field">
        <label className="add-label" htmlFor={`${ids}-tags`}>
          Tags
        </label>
        <input
          id={`${ids}-tags`}
          className="add-input"
          value={tagText}
          onChange={(event) => setTagText(event.target.value)}
          placeholder="env:prod, team:payments"
        />
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

      {problem !== null && (
        /* `role="alert"` because it appears in response to the user's own
           press and has to be heard without them going looking for it. */
        <p className="add-field-error" role="alert">
          {problem}
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
