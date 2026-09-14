import { useId, useState } from "react";
import { MIN_PASSWORD_LENGTH } from "./api";
import { describeStrength, passwordStrength } from "./password";

/**
 * The email-and-password form, shared by sign-in and first-run setup.
 *
 * One component for both because they are the same form with different copy
 * and one extra affordance: setup shows how long the password is, sign-in
 * does not. Two components would be two chances for the autocomplete hints,
 * the field-error placement and the disabled-while-submitting rule to drift,
 * and those are precisely the details nobody re-checks on the screen they did
 * not touch.
 *
 * Presentational: it owns its field values and nothing else. The caller owns
 * the network, which is what lets a test drive every state — idle, submitting,
 * rejected on a field, rejected globally — without a server.
 */

/** A server rejection, placed under the input it blames when it names one. */
export type Rejection = {
  message: string;
  /** The JSON field name from the API, e.g. "email" or "password". */
  field?: string;
};

export type CredentialsFormProps = {
  /** Which form this is; decides the copy and the strength meter. */
  mode: "login" | "setup";
  onSubmit: (email: string, password: string) => void;
  submitting?: boolean;
  rejection?: Rejection | null;
};

export function CredentialsForm({
  mode,
  onSubmit,
  submitting = false,
  rejection = null,
}: CredentialsFormProps) {
  const ids = useId();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const setup = mode === "setup";
  /*
   * Only the setup form blocks submission on length.
   *
   * On the sign-in form the rule is the server's alone: an account created
   * before the minimum changed must still be able to log in, and a client
   * that refuses to send the password would lock that person out of their own
   * instance with a message about a rule they never agreed to.
   */
  const tooShort = setup && passwordStrength(password) === "short";
  const disabled = submitting || email.trim() === "" || password === "" || tooShort;

  // `aria-invalid` alone says "something here is wrong" without saying what,
  // so the message is tied to the input it blames with `aria-describedby`.
  const badField = rejection?.field;
  const errorId = `${ids}-error`;

  return (
    <form
      className="auth-form"
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        if (disabled) return;
        onSubmit(email.trim(), password);
      }}
    >
      <div className="auth-field">
        <label className="auth-label" htmlFor={`${ids}-email`}>
          Email
        </label>
        <input
          id={`${ids}-email`}
          className="auth-input"
          type="email"
          name="email"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          // The browser's own password manager is the one that will store
          // this, and it needs the standard tokens to offer to do so.
          autoComplete={setup ? "email" : "username"}
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          // Setup is the first screen of the instance and has exactly one
          // starting point; autofocus there costs nothing and saves a click.
          // On the login form it would steal focus from a password manager
          // that had already filled both fields.
          //
          // jsx-a11y/no-autofocus is waived here rather than globally,
          // because the objection it raises is real everywhere else: moving
          // focus without being asked disorients a screen-reader user who has
          // not finished hearing the page. It does not apply to a page whose
          // entire content is this one field, and the expression is `setup`,
          // not `true`, so the login form is still left alone.
          // oxlint-disable-next-line jsx-a11y/no-autofocus
          autoFocus={setup}
          required
          aria-invalid={badField === "email" ? true : undefined}
          aria-describedby={badField === "email" ? errorId : undefined}
        />
      </div>

      <div className="auth-field">
        <label className="auth-label" htmlFor={`${ids}-password`}>
          Password
        </label>
        <input
          id={`${ids}-password`}
          className="auth-input"
          type="password"
          name="password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          autoComplete={setup ? "new-password" : "current-password"}
          required
          minLength={setup ? MIN_PASSWORD_LENGTH : undefined}
          aria-invalid={badField === "password" ? true : undefined}
          aria-describedby={
            [
              badField === "password" ? errorId : null,
              setup ? `${ids}-strength` : null,
            ]
              .filter((id) => id !== null)
              .join(" ") || undefined
          }
        />
        {setup && (
          <>
            {/*
              * A sentence, not a coloured bar.
              *
              * The bar is the part people read as a score to beat, and it
              * carries its meaning in hue alone — which is the one channel
              * some of the audience does not have. The words say the same
              * thing to everyone, and the data attribute carries the band for
              * the stylesheet without the text depending on it.
              */}
            <p className="auth-strength" id={`${ids}-strength`} data-band={passwordStrength(password)}>
              {describeStrength(password)}
            </p>
            {/*
              * Announced separately and politely: the paragraph above changes
              * on every keystroke, and a live region on it would make a
              * screen reader read the whole sentence letter by letter.
              */}
            <p className="auth-sr-only" role="status">
              {passwordStrength(password) === "short" ? "" : describeStrength(password)}
            </p>
          </>
        )}
      </div>

      {rejection !== null && (
        /*
         * One error region, whether or not the server named a field.
         *
         * `role="alert"` because it appears in response to the user's own
         * submission and is the reason nothing happened; a passive region
         * would let a screen-reader user press the button and hear silence.
         */
        <p className="auth-error" id={errorId} role="alert">
          {rejection.message}
        </p>
      )}

      <button className="auth-submit" type="submit" disabled={disabled}>
        {submitting
          ? setup
            ? "Creating your account\u2026"
            : "Signing in\u2026"
          : setup
            ? "Create account"
            : "Sign in"}
      </button>
    </form>
  );
}
