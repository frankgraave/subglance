import { useEffect, useId, useRef, useState } from "react";
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

/**
 * A rejection the form has to show, and where it belongs.
 *
 * `field` is the JSON name the server blamed. It is deliberately not an enum
 * of the inputs this form happens to render: the API may name a field the
 * form has no control for (`ssl_warn_days`, say, which only the edit screen
 * will offer), and the honest answer there is to show the message globally
 * rather than to drop it because it did not match a known input.
 */
export type Rejection = {
  message: string;
  field?: string;
};

/**
 * Which input a server field name belongs to.
 *
 * The mapping exists because the two vocabularies are not the same and should
 * not be forced to be: the API names wire fields, the form names controls.
 * `keyword_mode` has no control of its own — it is set implicitly by typing a
 * keyword — so a complaint about it points at the keyword box, which is the
 * only thing the user can act on.
 */
const FIELD_CONTROL: Record<string, string> = {
  target: "target",
  name: "name",
  type: "type",
  interval_s: "interval",
  timeout_s: "timeout",
  keyword: "keyword",
  keyword_mode: "keyword",
};

/**
 * The controls that live inside the collapsed advanced panel.
 *
 * A message placed under one of these is invisible until the panel is opened,
 * and the form deliberately suppresses both of its global notices once a
 * message has been placed — so a rejection naming one of these fields used to
 * disappear entirely: the panel hid it and nothing else said anything. The set
 * is written down once rather than derived from the JSX, because the thing that
 * matters is *which controls are hidden*, and the markup cannot be asked that.
 */
const ADVANCED_CONTROLS = new Set(["type", "interval", "timeout", "keyword"]);

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
  saveError?: Rejection | null;
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

  /*
   * The one rejection currently on screen, whichever half produced it.
   *
   * A save error wins over a preview one because it is the more recent answer
   * to the more committing question; only one can be true of the form as it
   * stands, and showing both would leave two red boxes contradicting each
   * other about the same input.
   */
  const rejection: Rejection | null =
    saveError ?? (preview.phase === "rejected" ? preview : null);
  const badControl =
    rejection?.field !== undefined
      ? (FIELD_CONTROL[rejection.field] ?? null)
      : null;

  /*
   * Whether the advanced panel is open.
   *
   * Uncontrolled in the ordinary case — `<details>` already toggles itself, and
   * forcing `open` from state would fight the user's click. State exists only
   * so a *new* rejection about a hidden control can push it open once; after
   * that the panel is theirs again and closing it sticks.
   */
  const [advancedOpen, setAdvancedOpen] = useState(false);
  /*
   * The rejection the panel was last opened for.
   *
   * Keyed on the message as well as the control, because two different
   * complaints about the same box are two different answers and the second one
   * deserves to be seen as much as the first.
   */
  const openedFor = useRef<string | null>(null);
  const hiddenRejection =
    badControl !== null && ADVANCED_CONTROLS.has(badControl)
      ? `${badControl}:${rejection?.message ?? ""}`
      : null;

  useEffect(() => {
    if (hiddenRejection === null) {
      openedFor.current = null;
      return;
    }
    if (openedFor.current === hiddenRejection) return;
    openedFor.current = hiddenRejection;
    setAdvancedOpen(true);
  }, [hiddenRejection]);

  /*
   * Props that mark an input as the one at fault.
   *
   * `aria-invalid` alone says "something here is wrong" without saying what,
   * so the message is tied on with `aria-describedby` and the field's own
   * help text is kept in the list — the explanation of the format is at least
   * as useful when you have just got it wrong.
   */
  const invalidProps = (control: string, describedBy: string) =>
    badControl === control
      ? {
          "aria-invalid": true,
          "aria-describedby": `${describedBy} ${ids}-field-error`,
        }
      : { "aria-describedby": describedBy };

  const setTarget = (target: string) => setValues((v) => ({ ...v, target }));

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (targetEmpty || saving) return;
    onSubmit(effective);
  };

  return (
    <form
      className="add-form"
      onSubmit={submit}
      aria-labelledby={`${ids}-heading`}
    >
      <h2 id={`${ids}-heading`} className="add-title">
        Add a monitor
      </h2>
      <p className="add-lede">
        Paste an address. SubGlance works out what kind of check it is and fills
        in the rest — you can change any of it below.
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
          {...invalidProps("target", `${ids}-target-help`)}
        />
        <p id={`${ids}-target-help`} className="add-help">
          A URL, a hostname, or a host and port. A bare hostname is checked over
          HTTPS.
        </p>
        <FieldError
          control="target"
          badControl={badControl}
          rejection={rejection}
          ids={ids}
        />
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
          {...invalidProps("name", `${ids}-name-help`)}
        />
        <FieldError
          control="name"
          badControl={badControl}
          rejection={rejection}
          ids={ids}
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
      {/*
        `open` is driven from state only so a rejection about a control in here
        can reveal it; `onToggle` writes the user's own clicks straight back, so
        the two never disagree about what is on screen.
      */}
      <details
        className="add-advanced"
        open={advancedOpen}
        onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
      >
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
              onChange={(event) =>
                setValues((v) => ({ ...v, type: event.target.value }))
              }
              aria-invalid={badControl === "type" ? true : undefined}
              aria-describedby={
                badControl === "type" ? `${ids}-field-error` : undefined
              }
            >
              <option value="">Work it out from the address</option>
              <option value="http">HTTP</option>
              <option value="tcp">TCP</option>
              <option value="ping">Ping</option>
              <option value="ssl">TLS certificate</option>
            </select>
            <FieldError
              control="type"
              badControl={badControl}
              rejection={rejection}
              ids={ids}
            />
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
                  setValues((v) => ({
                    ...v,
                    intervalS: Number(event.target.value),
                  }))
                }
                aria-invalid={badControl === "interval" ? true : undefined}
                aria-describedby={
                  badControl === "interval" ? `${ids}-field-error` : undefined
                }
              />
              {/* Units live in an addon on the field, not in the label (§7.2). */}
              <span className="add-unit" aria-hidden="true">
                sec
              </span>
            </div>
            <FieldError
              control="interval"
              badControl={badControl}
              rejection={rejection}
              ids={ids}
            />
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
                  setValues((v) => ({
                    ...v,
                    timeoutS: Number(event.target.value),
                  }))
                }
                aria-invalid={badControl === "timeout" ? true : undefined}
                aria-describedby={
                  badControl === "timeout" ? `${ids}-field-error` : undefined
                }
              />
              <span className="add-unit" aria-hidden="true">
                sec
              </span>
            </div>
            <FieldError
              control="timeout"
              badControl={badControl}
              rejection={rejection}
              ids={ids}
            />
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
              aria-invalid={badControl === "keyword" ? true : undefined}
              aria-describedby={
                badControl === "keyword" ? `${ids}-field-error` : undefined
              }
            />
            <FieldError
              control="keyword"
              badControl={badControl}
              rejection={rejection}
              ids={ids}
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
        <button
          type="submit"
          className="add-button add-button-primary"
          disabled={targetEmpty || saving}
        >
          {saving ? "Saving…" : "Save monitor"}
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

      {/*
       * One live region for the preview, and it is polite.
       *
       * The result appears because the user pressed a button and is waiting
       * for it, so `assertive` would interrupt them to say the thing they
       * asked for. A save error is a different matter: it happened to them
       * rather than for them, and it gets `role="alert"` below.
       */}
      <div role="status" aria-live="polite" className="add-result-region">
        <PreviewNotice preview={preview} placed={badControl !== null} />
      </div>

      {/*
       * Whatever could not be placed. A rate limit, a network failure and a
       * malformed response are not about any input, and neither is a
       * complaint about a field this form does not render — putting those
       * under an arbitrary box would be worse than leaving them here.
       */}
      {saveError !== null && badControl === null && (
        <p role="alert" className="add-result add-result-bad">
          Could not save: {saveError.message}
        </p>
      )}
    </form>
  );
}

/**
 * The message for one input, rendered only beneath the input at fault.
 *
 * `role="alert"` rather than a live region on a wrapper: the element appears
 * when the error does, so there is nothing to announce until it exists, and an
 * empty live region that later fills is the pattern screen readers most often
 * miss. It carries a stable id because the input points at it with
 * `aria-describedby`, and only one can be on screen at a time — the server
 * rejects on the first problem it finds, so a second id would never resolve.
 */
function FieldError({
  control,
  badControl,
  rejection,
  ids,
}: {
  control: string;
  badControl: string | null;
  rejection: Rejection | null;
  ids: string;
}) {
  if (badControl !== control || rejection === null) return null;
  return (
    <p id={`${ids}-field-error`} role="alert" className="add-field-error">
      {rejection.message}
    </p>
  );
}

function PreviewNotice({
  preview,
  placed,
}: {
  preview: PreviewState;
  placed: boolean;
}) {
  switch (preview.phase) {
    case "idle":
      return null;
    case "checking":
      return <p className="add-result">Checking…</p>;
    case "rejected":
      // Not a failed check: nothing was contacted. Saying so keeps someone
      // from going to look at a server that was never asked anything.
      //
      // Suppressed once the message has been placed under an input: the same
      // sentence in two places reads as two problems, and the copy down here
      // is the one that is further from the box you have to fix.
      if (placed) return null;
      return <p className="add-result add-result-bad">{preview.message}</p>;
    case "done":
      return <PreviewSummary result={preview.result} />;
  }
}

function PreviewSummary({ result }: { result: PreviewResult }) {
  return (
    <p
      className={
        result.ok ? "add-result add-result-good" : "add-result add-result-bad"
      }
    >
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
