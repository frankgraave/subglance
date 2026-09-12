import { useId, useState } from "react";
import type { FormEvent } from "react";
import type { PreviewResult, PreviewState } from "./preview";
import { describePreview, suggestName } from "./preview";

/**
 * Add a monitor in under sixty seconds (DESIGN.md §7.2, product principle 2).
 *
 * Three decisions carry this screen.
 *
 * **One required field.** Paste an address; everything else has a default the
 * server already applies. Type is not asked for — it is inferred from what was
 * pasted, and the preview tells the user what the inference decided, so the
 * dropdown that would otherwise be the second field disappears. Name is
 * suggested from the host and stays editable.
 *
 * **Test before save, not after.** Saving first and finding out later is the
 * shape that fails the sixty seconds: a missing scheme costs a save, a wait for
 * the first scheduled check, and an edit. The preview probes the real target
 * from the real server and writes nothing.
 *
 * **The advanced options are collapsed, not absent.** A `<details>` rather
 * than a second screen, because the person who needs a keyword check needs it
 * on the first monitor, not after discovering a settings page.
 *
 * Presentational: it owns its field values, and the caller owns the network.
 * That is what lets a test drive every preview phase without a fetch.
 */

export type AddMonitorValues = {
  name: string;
  target: string;
  /** Empty means "let the server infer it". */
  type: string;
  intervalS: number;
  timeoutS: number;
  keyword: string;
  keywordMode: string;
};

export type AddMonitorFormProps = {
  /** Runs a preview. The caller reports the outcome back through `preview`. */
  onPreview: (values: AddMonitorValues) => void;
  /** Saves. Called only from the submit button. */
  onSubmit: (values: AddMonitorValues) => void;
  /** The current preview phase, owned by the caller. */
  preview: PreviewState;
  /** True while a save is in flight. */
  saving?: boolean;
  /** A save that failed, as the server explained it. */
  saveError?: string | null;
  onCancel?: () => void;
};

const DEFAULTS: AddMonitorValues = {
  name: "",
  target: "",
  type: "",
  intervalS: 60,
  timeoutS: 10,
  keyword: "",
  keywordMode: "absent_ok",
};

export function AddMonitorForm({
  onPreview,
  onSubmit,
  preview,
  saving = false,
  saveError = null,
  onCancel,
}: AddMonitorFormProps) {
  const ids = useId();
  const [values, setValues] = useState<AddMonitorValues>(DEFAULTS);
  /*
   * The name, once someone has typed one.
   *
   * Until then it is null and the field shows a suggestion derived from the
   * target — that is what makes the name free rather than a second thing to
   * fill in. The moment it is edited it becomes theirs and the target stops
   * overwriting it.
   *
   * State rather than a ref holding a "touched" flag, even though the flag
   * would never need to re-render on its own: the field's displayed value is
   * derived from it, so it is render input by definition, and a ref read
   * during render is exactly the pattern that leaves the input showing a
   * stale value after a concurrent re-render.
   */
  const [typedName, setTypedName] = useState<string | null>(null);

  const targetEmpty = values.target.trim() === "";
  const name = typedName ?? suggestName(values.target);
  const effective = { ...values, name };

  const setTarget = (target: string) => setValues((v) => ({ ...v, target }));

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (targetEmpty || saving) return;
    onSubmit(effective);
  };

  return (
    <form className="add-form" onSubmit={submit} aria-labelledby={`${ids}-heading`}>
      <h2 id={`${ids}-heading`} className="add-title">
        Add a monitor
      </h2>
      <p className="add-lede">
        Paste an address. SubGlance works out what kind of check it is and fills in the rest — you
        can change any of it below.
      </p>

      <div className="add-field">
        <label className="add-label" htmlFor={`${ids}-target`}>
          What should be watched
        </label>
        <input
          id={`${ids}-target`}
          className="add-input"
          value={values.target}
          onChange={(event) => setTarget(event.target.value)}
          placeholder="example.com, https://example.com/health, or db.example.com:5432"
          autoComplete="off"
          spellCheck={false}
          required
          aria-describedby={`${ids}-target-help`}
        />
        <p id={`${ids}-target-help`} className="add-help">
          A URL, a hostname, or a host and port. A bare hostname is checked over HTTPS.
        </p>
      </div>

      <div className="add-field">
        <label className="add-label" htmlFor={`${ids}-name`}>
          Name
        </label>
        <input
          id={`${ids}-name`}
          className="add-input"
          value={name}
          onChange={(event) => setTypedName(event.target.value)}
          placeholder="Taken from the address"
          autoComplete="off"
          aria-describedby={`${ids}-name-help`}
        />
        <p id={`${ids}-name-help`} className="add-help">
          Optional. Left alone, it follows the address.
        </p>
      </div>

      {/*
       * Collapsed, and collapsed by default. The fields below are real — a
       * keyword check is the difference between "the server is up" and "the
       * site works" — but every one of them shown up front is a question asked
       * of someone who has not yet seen the product do anything.
       */}
      <details className="add-advanced">
        <summary className="add-summary">Advanced options</summary>

        <div className="add-grid">
          <div className="add-field">
            <label className="add-label" htmlFor={`${ids}-type`}>
              Check type
            </label>
            <select
              id={`${ids}-type`}
              className="add-input"
              value={values.type}
              onChange={(event) => setValues((v) => ({ ...v, type: event.target.value }))}
            >
              <option value="">Work it out from the address</option>
              <option value="http">HTTP</option>
              <option value="tcp">TCP</option>
              <option value="ping">Ping</option>
              <option value="ssl">TLS certificate</option>
            </select>
          </div>

          <div className="add-field">
            <label className="add-label" htmlFor={`${ids}-interval`}>
              Check every
            </label>
            <div className="add-addon">
              <input
                id={`${ids}-interval`}
                className="add-input"
                type="number"
                min={20}
                max={86400}
                value={values.intervalS}
                onChange={(event) =>
                  setValues((v) => ({ ...v, intervalS: Number(event.target.value) }))
                }
              />
              {/* Units live in an addon on the field, not in the label (§7.2). */}
              <span className="add-unit" aria-hidden="true">
                sec
              </span>
            </div>
          </div>

          <div className="add-field">
            <label className="add-label" htmlFor={`${ids}-timeout`}>
              Give up after
            </label>
            <div className="add-addon">
              <input
                id={`${ids}-timeout`}
                className="add-input"
                type="number"
                min={1}
                max={120}
                value={values.timeoutS}
                onChange={(event) =>
                  setValues((v) => ({ ...v, timeoutS: Number(event.target.value) }))
                }
              />
              <span className="add-unit" aria-hidden="true">
                sec
              </span>
            </div>
          </div>

          <div className="add-field add-field-wide">
            <label className="add-label" htmlFor={`${ids}-keyword`}>
              Body must contain
            </label>
            <input
              id={`${ids}-keyword`}
              className="add-input"
              value={values.keyword}
              onChange={(event) =>
                setValues((v) => ({
                  ...v,
                  keyword: event.target.value,
                  // A keyword with the mode left at "ignore" is a field that
                  // silently does nothing — the exact trap DESIGN.md §7.2 is
                  // about. Typing one means you want it checked.
                  keywordMode:
                    v.keywordMode === "absent_ok" && event.target.value !== ""
                      ? "must_contain"
                      : v.keywordMode,
                }))
              }
              placeholder="Leave empty to check only that it answers"
              autoComplete="off"
            />
          </div>
        </div>
      </details>

      <div className="add-actions">
        <button
          type="button"
          className="add-button"
          onClick={() => onPreview(effective)}
          // Deliberately NOT disabled while a probe is in flight. A check can
          // take the full timeout, and the most common reason to press this
          // twice is that the typo became obvious the moment the first one
          // started — locking the button makes the user wait out a request
          // whose answer they already know is useless. The caller aborts the
          // previous probe, so a late answer cannot overwrite a newer one.
          disabled={targetEmpty}
        >
          {/* The label keeps the button's width rather than swapping in a
              spinner that resizes it (§7.1). */}
          {preview.phase === "checking" ? "Testing…" : "Test it"}
        </button>
        <button type="submit" className="add-button add-button-primary" disabled={targetEmpty || saving}>
          {saving ? "Saving…" : "Save monitor"}
        </button>
        {onCancel !== undefined && (
          <button type="button" className="add-button add-button-quiet" onClick={onCancel}>
            Cancel
          </button>
        )}
      </div>

      {/*
       * One live region for the preview, and it is polite.
       *
       * The result appears because the user pressed a button and is waiting
       * for it, so `assertive` would interrupt them to say the thing they
       * asked for. A save error is a different matter: it happened to them
       * rather than for them, and it gets `role="alert"` below.
       */}
      <div role="status" aria-live="polite" className="add-result-region">
        <PreviewNotice preview={preview} />
      </div>

      {saveError !== null && (
        <p role="alert" className="add-result add-result-bad">
          Could not save: {saveError}
        </p>
      )}
    </form>
  );
}

function PreviewNotice({ preview }: { preview: PreviewState }) {
  switch (preview.phase) {
    case "idle":
      return null;
    case "checking":
      return <p className="add-result">Checking…</p>;
    case "rejected":
      // Not a failed check: nothing was contacted. Saying so keeps someone
      // from going to look at a server that was never asked anything.
      return <p className="add-result add-result-bad">{preview.message}</p>;
    case "done":
      return <PreviewSummary result={preview.result} />;
  }
}

function PreviewSummary({ result }: { result: PreviewResult }) {
  return (
    <p className={result.ok ? "add-result add-result-good" : "add-result add-result-bad"}>
      {describePreview(result)}{" "}
      <span className="add-result-kind">
        {/* What it decided to check, spelled out. This is the field the form
            stopped asking for, so it has to be visible somewhere before the
            user commits. */}
        Checked as {labelForType(result.type)}.
      </span>
    </p>
  );
}

function labelForType(type: string): string {
  switch (type) {
    case "http":
      return "an HTTP request";
    case "tcp":
      return "a TCP connection";
    case "ping":
      return "a ping";
    case "ssl":
      return "a TLS certificate check";
    default:
      return type;
  }
}
