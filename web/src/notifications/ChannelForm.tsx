import { useId, useRef, useState } from "react";
import type { FormEvent } from "react";
import { CHANNEL_TYPES, fieldsFor, hasSecret, typeLabel } from "./channels";
import type { Channel, ChannelType, FieldSpec } from "./channels";
import type { ChannelInput } from "./channelsApi";

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
 * Presentational: it owns its field values, the caller owns the network, so a
 * test drives saving and rejection without a fetch.
 */

export type ChannelFormProps = {
  /** The channel being edited, or null when adding. */
  channel?: Channel | null;
  onSave: (input: ChannelInput) => Promise<void>;
  onCancel?: () => void;
};

type Problem = { message: string; key: string | null } | null;

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
   * Keyed by config key rather than held per type, so switching type on a new
   * channel does not silently carry a Slack URL into an e-mail address — the
   * keys simply do not overlap, and the ones that do (nothing today) would be
   * the same setting by the same name.
   */
  const [values, setValues] = useState<Record<string, string>>(() => {
    const seeded: Record<string, string> = {};
    for (const [key, value] of Object.entries(channel?.config ?? {})) {
      seeded[key] = value;
    }
    return seeded;
  });
  /*
   * Which secrets the user chose to replace, and what they typed.
   *
   * Absent from this map means "leave it alone": the stored value is echoed
   * back as its mask on save. Present means the control is accepting input and
   * whatever is in it — including an empty string — is what gets sent.
   */
  const [replacing, setReplacing] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<Problem>(null);
  const nameRef = useRef<HTMLInputElement>(null);
  const fieldRefs = useRef<Record<string, HTMLInputElement | null>>({});

  const specs = fieldsFor(type);

  const reject = (message: string, key: string | null = null) => {
    setProblem({ message, key });
    if (key === "name") nameRef.current?.focus();
    else if (key !== null) fieldRefs.current[key]?.focus();
  };

  /** Whether this field currently accepts typing. */
  const accepting = (spec: FieldSpec) =>
    !spec.secret || spec.key in replacing || !storedSecret(spec);

  /** Whether the channel already holds a secret for this field. */
  const storedSecret = (spec: FieldSpec) =>
    editing && channel !== null && spec.secret && hasSecret(channel, spec.key);

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
      if (spec.secret && storedSecret(spec) && !(spec.key in replacing)) {
        /*
         * Untouched: echo the mask back. The server restores the stored value
         * when it sees a config entry that still equals its own mask, so this
         * is the only spelling of "leave this credential alone" that the PUT
         * contract accepts.
         */
        config[spec.key] = channel?.config[spec.key] ?? "";
        continue;
      }
      const raw = spec.secret
        ? (replacing[spec.key] ?? values[spec.key] ?? "")
        : (values[spec.key] ?? "");
      const value = spec.key === "headers" ? raw : raw.trim();
      if (spec.required && value === "") {
        reject(`${spec.label} is required for a ${typeLabel(type)} channel.`, spec.key);
        return;
      }
      if (value !== "") config[spec.key] = value;
    }

    setSaving(true);
    void (async () => {
      try {
        await onSave({ name: trimmedName, type, config });
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

  return (
    <form className="add-form" onSubmit={submit}>
      {/*
       * Type is a select, and it is fixed once the channel exists.
       *
       * PUT accepts a type change, but the field set changes with it: turning
       * a Slack channel into an e-mail one would carry a masked webhook URL
       * into a form that has no box for it and then send it as the recipient.
       * Delete and recreate is the honest path, and it is one the user can see
       * the consequence of.
       */}
      <div className="add-field">
        {/*
         * Same rule as the stored-secret field below: while the type is stated
         * rather than chosen, there is no select for the label to point at.
         * This one was not in the review — it was found by asserting the
         * property across the whole form instead of fixing the single case
         * that was reported.
         */}
        {editing ? (
          <p className="add-label">Type</p>
        ) : (
          <label className="add-label" htmlFor={`${ids}-type`}>
            Type
          </label>
        )}
        {editing ? (
          <p className="add-help">
            <b>{typeLabel(type)}</b> — a channel's type cannot be changed here.
            Each type stores different settings, so switching one would ask you
            to re-enter every field including the credential. Delete this
            channel and add the other kind instead.
          </p>
        ) : (
          <select
            id={`${ids}-type`}
            className="mon-facet-select"
            value={type}
            onChange={(event) => {
              setType(event.target.value as ChannelType);
              setProblem(null);
            }}
          >
            {CHANNEL_TYPES.map((value) => (
              <option key={value} value={value}>
                {typeLabel(value)}
              </option>
            ))}
          </select>
        )}
      </div>

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
          placeholder="On-call Slack"
          {...(problem?.key === "name"
            ? {
                "aria-invalid": true as const,
                "aria-describedby": `${ids}-name-error`,
              }
            : {})}
        />
        {problem?.key === "name" && (
          <p className="add-field-error" id={`${ids}-name-error`} role="alert">
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
        return (
          <div className="add-field" key={spec.key}>
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
              <p className="add-label" id={`${inputId}-name`}>
                {spec.label}
                {!spec.required && " (optional)"}
              </p>
            ) : (
              <label className="add-label" htmlFor={inputId}>
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
                <p className="add-help" id={`${inputId}-state`}>
                  A value is stored. It is never sent back to this page, so it
                  cannot be shown or copied — only replaced.{" "}
                  {channel !== null && (
                    <span className="nt-mask">{channel.config[spec.key]}</span>
                  )}
                </p>
                <div className="add-actions">
                  <button
                    type="button"
                    className="add-button nt-act"
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
                <input
                  id={inputId}
                  ref={(node) => {
                    fieldRefs.current[spec.key] = node;
                  }}
                  className="add-input"
                  /*
                   * A control that is accepting a secret is a password field
                   * for as long as it accepts one. Not because the value is
                   * unreadable to the person typing it — they pasted it — but
                   * because this page is opened during screen shares and over
                   * shoulders, and the value stays on screen until save.
                   */
                  type={spec.secret ? "password" : "text"}
                  autoComplete={spec.secret ? "new-password" : "off"}
                  value={
                    spec.secret
                      ? (replacing[spec.key] ?? values[spec.key] ?? "")
                      : (values[spec.key] ?? "")
                  }
                  onChange={(event) => {
                    const next = event.target.value;
                    if (spec.secret) {
                      setReplacing((current) => ({
                        ...current,
                        [spec.key]: next,
                      }));
                    } else {
                      setValues((current) => ({
                        ...current,
                        [spec.key]: next,
                      }));
                    }
                  }}
                  {...(spec.placeholder !== undefined
                    ? { placeholder: spec.placeholder }
                    : {})}
                  {...(invalid
                    ? {
                        "aria-invalid": true as const,
                        "aria-describedby": `${inputId}-error`,
                      }
                    : {})}
                />
                {invalid && (
                  <p
                    className="add-field-error"
                    id={`${inputId}-error`}
                    role="alert"
                  >
                    {problem.message}
                  </p>
                )}
                {stored && (
                  <div className="add-actions">
                    <button
                      type="button"
                      className="add-button add-button-quiet nt-act"
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
              <p className="add-help">{spec.help}</p>
            )}
          </div>
        );
      })}

      {/*
       * What this form does not offer, and why.
       *
       * Saying it is cheaper than the alternative: a reviewer comparing this
       * with the mockup would otherwise read the missing controls as an
       * oversight and add them, and each one would save a setting the notifier
       * never reads.
       */}
      <p className="add-help">
        The settings shown are the ones the senders in{" "}
        <code>internal/notifier</code> actually read. The mockup also draws a
        Slack channel label, an HTTP method and a webhook signing secret; none
        of those exist on the wire, so offering them would store values nothing
        would ever use. Which monitors alert through this channel is set on the
        monitor, not here.
      </p>

      {problem !== null && problem.key === null && (
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
          {saving ? "Saving…" : editing ? "Save changes" : "Add channel"}
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
