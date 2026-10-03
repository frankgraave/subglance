import { useId, useRef, useState } from "react";
import type { FormEvent } from "react";
import { Checkbox } from "../components/Choice";
import {
  CHANNEL_TYPES,
  fieldValue,
  fieldsFor,
  hasSecret,
  isMaskedList,
  listEntries,
  typeLabel,
  visibleFields,
} from "./channels";
import type { Channel, ChannelType, FieldSpec, QuietHours } from "./channels";
import type { ChannelInput } from "./channelsApi";
import { QuietHoursField } from "./QuietHoursField";
import { draftFrom, quietChange, quietProblem } from "./quietHours";
import { Select } from "../components/Select";

/**
 * Adding or editing one channel.
 *
 * **A stored secret is never rendered, and there is no reveal.** A reveal
 * button is a leak with an extra click. An existing secret is shown as the
 * statement "a value is stored", with one way to change it: Replace, which
 * clears the control and takes a fresh value. While that control accepts
 * input it is `type="password"`, because this screen gets opened on shared
 * displays and during screen shares.
 *
 * **An untouched secret is sent back exactly as it arrived.** The API masks
 * secrets on read and `handleUpdateChannel` treats a value that still equals
 * its own mask as "unchanged" — so echoing the mask is what leaves the stored
 * credential alone. Sending an empty string would wipe it; omitting the key
 * would fail validation for Slack, Discord, Telegram and webhook, whose only
 * required field *is* the secret.
 *
 * **Masked phone numbers get the same treatment as a secret.** An SMS
 * channel's numbers reach an editor or viewer as `+31 6 •••• 5678`. They are
 * not a credential, but the rule that protects a credential applies unchanged:
 * shown as they arrived, echoed back on save (the API reads its own mask as
 * "unchanged"), and changed only by replacing the whole list.
 *
 * Presentational: it owns its field values, the caller owns the network, so a
 * test drives saving and rejection without a fetch.
 */

export type ChannelFormProps = {
  /** The channel being edited, or null when adding. */
  channel?: Channel | null;
  /**
   * Saves the channel. `quiet` is undefined when the window is unchanged,
   * null to remove it, or the window to store.
   */
  onSave: (
    input: ChannelInput,
    quiet?: QuietHours | null,
  ) => Promise<void>;
  onCancel?: () => void;
};

type Problem = { message: string; key: string | null } | null;

/** The problem key for the quiet-hours fields, which have no config key. */
const QUIET = "quiet_hours";

/**
 * What quiet hours cost on a type, where that is worth saying before the box
 * is ticked. Only SMS today: it is chosen because it wakes someone, and a
 * window that holds it until morning undoes that choice.
 */
const QUIET_CAVEAT: Partial<Record<ChannelType, string>> = {
  sms: "On an SMS channel, think twice: a text held until morning usually defeats the reason for choosing SMS.",
};

type FieldControl = HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement;

/** Seeds the form from a stored config: a list reads one entry per line. */
function seedValues(channel: Channel | null): Record<string, string> {
  const seeded: Record<string, string> = {};
  const specs = fieldsFor(channel?.type ?? "");
  for (const [key, value] of Object.entries(channel?.config ?? {})) {
    const spec = specs.find((candidate) => candidate.key === key);
    seeded[key] =
      spec?.control === "list" && !isMaskedList(value)
        ? listEntries(value).join("\n")
        : value;
  }
  return seeded;
}

export function ChannelForm({
  channel = null,
  onSave,
  onCancel,
}: ChannelFormProps) {
  const ids = useId();
  const editing = channel !== null;

  const [type, setType] = useState<ChannelType>(() => {
    const current = channel?.type;
    return current !== undefined &&
      (CHANNEL_TYPES as readonly string[]).includes(current)
      ? (current as ChannelType)
      : "email";
  });
  const [name, setName] = useState(channel?.name ?? "");
  /*
   * Values for the non-secret fields, seeded from the channel being edited.
   *
   * Keyed by config key, and emptied when the type of a new channel changes.
   * Keys do overlap between types (`url` is a Gotify server, an ntfy server
   * and an SMS gateway; `from` is an e-mail sender and a Twilio sender), and
   * carrying one type's value into another's field would offer a setting that
   * was typed for something else.
   */
  const [values, setValues] = useState<Record<string, string>>(() =>
    seedValues(channel),
  );
  /*
   * Which secrets the user chose to replace, and what they typed.
   *
   * Absent from this map means "leave it alone": the stored value is echoed
   * back as its mask on save. Present means the control is accepting input and
   * whatever is in it — including an empty string — is what gets sent.
   */
  const [replacing, setReplacing] = useState<Record<string, string>>({});
  const storedQuiet = channel?.quietHours ?? null;
  const [quiet, setQuiet] = useState(() => draftFrom(storedQuiet));
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<Problem>(null);
  const nameRef = useRef<HTMLInputElement>(null);
  const fieldRefs = useRef<Record<string, FieldControl | null>>({});

  const allSpecs = fieldsFor(type);
  // Fields tied to another field's value (an SMS provider's credentials) are
  // left out, and so neither checked nor sent, while that value is not chosen.
  const specs = visibleFields(type, values);

  const reject = (message: string, key: string | null = null) => {
    setProblem({ message, key });
    if (key === "name") nameRef.current?.focus();
    else if (key !== null) fieldRefs.current[key]?.focus();
  };

  /** Whether this field currently accepts typing. */
  const accepting = (spec: FieldSpec) =>
    spec.key in replacing || !storedSecret(spec);

  /**
   * Whether the channel holds a value this page was not shown: a stored
   * secret, or personal data the API masked for this reader.
   */
  const storedSecret = (spec: FieldSpec) => {
    if (!editing || channel === null) return false;
    if (spec.secret) return hasSecret(channel, spec.key);
    return spec.personal === true && isMaskedList(channel.config[spec.key] ?? "");
  };

  /** What a control shows: what was typed over a replaced value, or the value. */
  const shown = (spec: FieldSpec) =>
    spec.key in replacing ? replacing[spec.key] : (values[spec.key] ?? "");

  const edit = (spec: FieldSpec, next: string) => {
    if (spec.secret || spec.key in replacing) {
      setReplacing((current) => ({ ...current, [spec.key]: next }));
    } else {
      setValues((current) => ({ ...current, [spec.key]: next }));
    }
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setProblem(null);

    const trimmedName = name.trim();
    if (trimmedName === "") {
      reject("A channel needs a name — it is how you recognise it.", "name");
      return;
    }

    const config: Record<string, string> = {};
    for (const spec of specs) {
      if (spec.control === "checkbox") {
        // Always sent, so the stored setting says what the box showed.
        config[spec.key] = values[spec.key] === "false" ? "false" : "true";
        continue;
      }
      if (spec.control === "select") {
        // A select shows its first option until another is picked, and
        // saving sends what it shows.
        config[spec.key] = fieldValue(allSpecs, values, spec.key);
        continue;
      }
      if (storedSecret(spec) && !(spec.key in replacing)) {
        /*
         * Untouched: echo the mask back. The server restores the stored value
         * when it sees a config entry that still equals its own mask, so this
         * is the only spelling of "leave this credential alone" that the PUT
         * contract accepts.
         */
        config[spec.key] = channel?.config[spec.key] ?? "";
        continue;
      }
      const raw = shown(spec);
      const value =
        spec.key === "headers"
          ? raw
          : spec.control === "list"
            ? listEntries(raw).join(", ")
            : raw.trim();
      if (spec.required && value === "") {
        const label = typeLabel(type);
        const article = /^(SMS|[AEIOU])/.test(label) ? "an" : "a";
        reject(`${spec.label} is required for ${article} ${label} channel.`, spec.key);
        return;
      }
      if (value !== "") config[spec.key] = value;
    }

    /*
     * Only a window that is about to be sent is checked. An unchanged stored
     * window is not re-sent, and the server judged its timezone against its
     * own zone data; this browser's Intl data may be older and must not block
     * a rename. Removal (null) has nothing to check.
     */
    const quietNext = quietChange(storedQuiet, quiet);
    const quietError = quietNext ? quietProblem(quiet) : null;
    if (quietError !== null) {
      setProblem({ message: quietError, key: QUIET });
      return;
    }

    setSaving(true);
    void (async () => {
      try {
        await onSave({ name: trimmedName, type, config }, quietNext);
      } catch (error) {
        /*
         * The server's own sentence, never pinned to a control.
         *
         * `validateChannel` names the field it refused in prose ("config.url
         * is not a valid URL"), and matching on that wording to place the
         * message under an input keeps working right up until someone rewords
         * it, and then fails silently — the text still renders, just in the
         * wrong place.
         */
        setProblem({
          message:
            error instanceof Error
              ? error.message
              : "the channel could not be saved",
          key: null,
        });
      } finally {
        setSaving(false);
      }
    })();
  };

  /** The control that takes a field's value, by the kind the spec names. */
  const renderControl = (spec: FieldSpec, inputId: string, invalid: boolean) => {
    const common = {
      id: inputId,
      className: "input",
      ref: (node: FieldControl | null) => {
        fieldRefs.current[spec.key] = node;
      },
      ...(invalid
        ? {
            "aria-invalid": true as const,
            "aria-describedby": `${inputId}-error`,
          }
        : {}),
    };
    if (spec.control === "select") {
      return (
        <Select
          {...common}
          value={fieldValue(allSpecs, values, spec.key)}
          onChange={(event) => {
            edit(spec, event.target.value);
            setProblem(null);
          }}
        >
          {(spec.options ?? []).map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </Select>
      );
    }
    if (spec.control === "list") {
      return (
        <textarea
          {...common}
          rows={3}
          spellCheck={false}
          autoComplete="off"
          value={shown(spec)}
          onChange={(event) => edit(spec, event.target.value)}
          {...(spec.placeholder !== undefined
            ? { placeholder: spec.placeholder }
            : {})}
        />
      );
    }
    return (
      <input
        {...common}
        /*
         * A control that is accepting a secret is a password field for as
         * long as it accepts one. Not because the value is unreadable to the
         * person typing it — they pasted it — but because this page is opened
         * during screen shares and over shoulders, and the value stays on
         * screen until save.
         */
        type={spec.secret ? "password" : "text"}
        autoComplete={spec.secret ? "new-password" : "off"}
        value={shown(spec)}
        onChange={(event) => edit(spec, event.target.value)}
        {...(spec.placeholder !== undefined
          ? { placeholder: spec.placeholder }
          : {})}
      />
    );
  };

  return (
    <form className="form-column" onSubmit={submit}>
      {/*
       * Type is a select, and it is fixed once the channel exists.
       *
       * PUT accepts a type change, but the field set changes with it: turning
       * a Slack channel into an e-mail one would carry a masked webhook URL
       * into a form that has no box for it and then send it as the recipient.
       * Delete and recreate is the honest path, and it is one the user can see
       * the consequence of.
       */}
      <div className="field">
        {/*
         * Same rule as the stored-secret field below: while the type is stated
         * rather than chosen, there is no select for the label to point at.
         * This one was not in the review — it was found by asserting the
         * property across the whole form instead of fixing the single case
         * that was reported.
         */}
        {editing ? (
          <p className="field-label">Type</p>
        ) : (
          <label className="field-label" htmlFor={`${ids}-type`}>
            Type
          </label>
        )}
        {editing ? (
          <p className="field-help">
            <b>{typeLabel(type)}</b> — a channel's type cannot be changed here.
            Each type stores different settings, so switching one would ask you
            to re-enter every field including the credential. Delete this
            channel and add the other kind instead.
          </p>
        ) : (
          <Select
            id={`${ids}-type`}
            className="input"
            value={type}
            onChange={(event) => {
              setType(event.target.value as ChannelType);
              setValues({});
              setReplacing({});
              setProblem(null);
            }}
          >
            {CHANNEL_TYPES.map((value) => (
              <option key={value} value={value}>
                {typeLabel(value)}
              </option>
            ))}
          </Select>
        )}
      </div>

      <div className="field">
        <label className="field-label" htmlFor={`${ids}-name`}>
          Name
        </label>
        <input
          id={`${ids}-name`}
          ref={nameRef}
          className="input"
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder="On-call Slack"
          {...(problem?.key === "name"
            ? {
                "aria-invalid": true as const,
                "aria-describedby": `${ids}-name-error`,
              }
            : {})}
        />
        {problem?.key === "name" && (
          <p className="field-error" id={`${ids}-name-error`} role="alert">
            {problem.message}
          </p>
        )}
      </div>

      {specs.map((spec) => {
        const stored = storedSecret(spec);
        const open = accepting(spec);
        const inputId = `${ids}-${spec.key}`;
        const invalid = problem?.key === spec.key;
        const showingStored = stored && !open;
        if (spec.control === "checkbox") {
          /*
           * A setting that is on or off, written as the sentence it turns on,
           * with the label around the box so the whole line is the target.
           * Absent means on: the sender's default for every such setting.
           */
          return (
            <div className="field" key={spec.key}>
              <Checkbox
                checked={values[spec.key] !== "false"}
                onChange={(event) =>
                  edit(spec, event.target.checked ? "true" : "false")
                }
                {...(spec.help !== undefined
                  ? { "aria-describedby": `${inputId}-help` }
                  : {})}
              >
                {spec.label}
              </Checkbox>
              {spec.help !== undefined && (
                <p className="field-help" id={`${inputId}-help`}>
                  {spec.help}
                </p>
              )}
            </div>
          );
        }
        return (
          <div className="field" key={spec.key}>
            {/*
             * A label is a promise that something is labelled.
             *
             * While a stored secret is shown there is no input in this field —
             * the only control is Replace — so `htmlFor` would point at an
             * element that does not exist. A screen reader then reads the
             * field name as loose text and clicking it does nothing, which is
             * worse than no label because both look correct. The name becomes
             * plain text and the button names itself after it instead.
             */}
            {showingStored ? (
              <p className="field-label" id={`${inputId}-name`}>
                {spec.label}
                {!spec.required && " (optional)"}
              </p>
            ) : (
              <label className="field-label" htmlFor={inputId}>
                {spec.label}
                {!spec.required && " (optional)"}
              </label>
            )}

            {showingStored ? (
              /*
               * A stored secret, stated rather than shown.
               *
               * The API returns `****` plus the last four characters, and that
               * tail is printed because it is what tells two Slack webhooks
               * apart — the reason `maskValue` keeps it. It is text, not an
               * input: there is nothing here to edit until Replace is pressed,
               * and a read-only box full of dots invites someone to try.
               */
              <>
                <p className="field-help" id={`${inputId}-state`}>
                  {spec.secret
                    ? "A value is stored. It is never sent back to this page, so it cannot be shown or copied — only replaced."
                    : "Only an administrator reads these in full. Saving keeps them as they are; replacing them means typing the whole list again."}{" "}
                  {channel !== null && (
                    <span className="literal">{channel.config[spec.key]}</span>
                  )}
                </p>
                <div className="button-row">
                  <button
                    type="button"
                    className="button button--compact"
                    aria-describedby={`${inputId}-state`}
                    onClick={() => {
                      setReplacing((current) => ({
                        ...current,
                        [spec.key]: "",
                      }));
                      // Focus lands on the new control in the next commit;
                      // the ref callback below is what has it by then.
                      queueMicrotask(() =>
                        fieldRefs.current[spec.key]?.focus(),
                      );
                    }}
                  >
                    Replace {spec.label.toLowerCase()}
                  </button>
                </div>
              </>
            ) : (
              <>
                {renderControl(spec, inputId, invalid)}
                {invalid && (
                  <p
                    className="field-error"
                    id={`${inputId}-error`}
                    role="alert"
                  >
                    {problem.message}
                  </p>
                )}
                {stored && (
                  <div className="button-row">
                    <button
                      type="button"
                      className="button button--quiet button--compact"
                      onClick={() =>
                        setReplacing((current) => {
                          const next = { ...current };
                          delete next[spec.key];
                          return next;
                        })
                      }
                    >
                      Keep the stored {spec.label.toLowerCase()}
                    </button>
                  </div>
                )}
              </>
            )}

            {spec.help !== undefined && (
              <p className="field-help">{spec.help}</p>
            )}
          </div>
        );
      })}

      <QuietHoursField
        {...(QUIET_CAVEAT[type] !== undefined
          ? { caveat: QUIET_CAVEAT[type] }
          : {})}
        value={quiet}
        onChange={(next) => {
          setQuiet(next);
          if (problem?.key === QUIET) setProblem(null);
        }}
        stored={storedQuiet !== null}
        error={problem?.key === QUIET ? problem.message : null}
      />

      {/*
       * What this form does not offer, and why.
       *
       * The settings shown are the ones the senders in `internal/notifier`
       * actually read. The design mockup also draws a Slack channel label, an
       * HTTP method and a webhook signing secret; none of those exist on the
       * wire, so offering them would store values nothing would ever use. That
       * reasoning stays here, for the next person comparing the two: the
       * sentence on screen used to carry it, and a package path and a mockup
       * mean nothing to the person filling in the form (SUB-193).
       *
       * What the reader does need is the one thing they will look for here and
       * not find: which monitors use this channel. A monitor's own channels
       * are chosen in its form since SUB-179; tag routing rules still have no
       * editor (SUB-158).
       */}
      <p className="field-help">
        Which monitors alert through this channel is not chosen here: each
        monitor chooses its own channels in its add and edit forms, and a tag
        routing rule, set through the API or an imported configuration file,
        can add more.
      </p>

      {problem !== null && problem.key === null && (
        <p className="field-error" role="alert">
          {problem.message}
        </p>
      )}

      <div className="button-row">
        <button
          type="submit"
          className="button button--primary"
          disabled={saving}
        >
          {saving ? "Saving…" : editing ? "Save changes" : "Add channel"}
        </button>
        {onCancel !== undefined && (
          <button
            type="button"
            className="button button--quiet"
            onClick={onCancel}
          >
            Cancel
          </button>
        )}
      </div>
    </form>
  );
}
